package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	golog "github.com/hirebarend/go-log"
	"github.com/hirebarend/raft-go/internal"
)

func main() {
	// if os.Getenv("ENV") == "PRODUCTION" {
	// 	gin.SetMode(gin.ReleaseMode)
	// }

	gin.SetMode(gin.ReleaseMode)

	data := flag.String("data", "data", "Path to the directory used for Raft's write-ahead log and persistent state.")
	nodes := flag.String("nodes", "127.0.0.1:8081,127.0.0.1:8082,127.0.0.1:8083", "Comma-separated list of cluster peer addresses (host:port).")
	port := flag.Int("port", 8081, "Port number on which this Raft node will listen for client and peer requests.")

	flag.Parse()

	addr := fmt.Sprintf("127.0.0.1:%d", *port)

	log := golog.NewLog[internal.LogEntry](*data, 64<<20)
	err := log.Load()

	if err != nil {
		panic(err)
	}

	store := internal.NewStore()
	raft := internal.NewRaft(addr, strings.Split(*nodes, ","), log, store, &internal.Transport{}, internal.NewFSM())

	r := gin.New()

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})

	r.GET("/disable", func(c *gin.Context) {
		raft.Disable()

		c.JSON(200, gin.H{"message": "disabled"})
	})

	r.GET("/enable", func(c *gin.Context) {
		raft.Enable()

		c.JSON(200, gin.H{"message": "enabled"})
	})

	r.POST("/append-entries", func(c *gin.Context) {
		var request internal.AppendEntriesRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
			return
		}

		term, success, conflictIndex, conflictTerm := raft.HandleAppendEntries(
			request.Term, request.LeaderId, request.PrevLogIndex, request.PrevLogTerm, request.Entries, request.LeaderCommit,
		)

		c.JSON(http.StatusOK, internal.AppendEntriesResponse{
			Term:          term,
			Success:       success,
			ConflictIndex: conflictIndex,
			ConflictTerm:  conflictTerm,
		})
	})

	r.POST("/pre-vote", func(c *gin.Context) {
		var request internal.PreVoteRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
			return
		}

		term, voteGranted := raft.HandlePreVote(
			request.Term,
			request.CandidateID,
			request.LastLogIndex,
			request.LastLogTerm,
		)

		c.JSON(http.StatusOK, internal.PreVoteResponse{
			Term:        term,
			VoteGranted: voteGranted,
		})
	})

	r.POST("/request-vote", func(c *gin.Context) {
		var request internal.RequestVoteRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
			return
		}

		term, voteGranted := raft.HandleRequestVote(
			request.Term,
			request.CandidateID,
			request.LastLogIndex,
			request.LastLogTerm,
		)

		c.JSON(http.StatusOK, internal.RequestVoteResponse{
			Term:        term,
			VoteGranted: voteGranted,
		})
	})

	r.POST("/propose", func(c *gin.Context) {
		result, err := raft.Propose(c, []byte(uuid.New().String()))

		c.JSON(http.StatusOK, gin.H{
			"ok":        err == nil,
			"result":    result,
			"leader_id": raft.GetLeaderId(),
		})
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := r.Run(addr); err != nil && err != http.ErrServerClosed {
			// log.Fatalf("listen: %s\n", err)
		}
	}()

	time.Sleep(5 * time.Second)

	go func() {
		ticker := time.NewTicker(25 * time.Millisecond) // TODO
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				raft.Tick()
			case <-ctx.Done():
				return
			}
		}
	}()

	<-ctx.Done()

	log.Close()
}
