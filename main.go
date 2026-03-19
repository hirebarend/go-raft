package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	golog "github.com/hirebarend/go-log"
	"github.com/hirebarend/go-raft/internal"
)

func main() {
	if os.Getenv("ENV") == "PRODUCTION" {
		gin.SetMode(gin.ReleaseMode)
	}

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

	store, err := internal.NewStore(filepath.Join(*data, "store.data"))

	if err != nil {
		panic(err)
	}

	raft := internal.NewRaft(addr, *data, strings.Split(*nodes, ","), log, store, internal.NewTransport(), internal.NewFSM())

	r := gin.Default()

	r.POST("/rpc/append-entries", func(c *gin.Context) {
		var request internal.AppendEntriesRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
			return
		}

		term, success, conflictIndex, conflictTerm := raft.HandleAppendEntries(
			request.Term, request.LeaderId, request.PrevLogEntryIndex, request.PrevLogEntryTerm, request.LogEntries, request.LeaderCommitIndex,
		)

		c.JSON(http.StatusOK, internal.AppendEntriesResponse{
			Term:          term,
			Success:       success,
			ConflictIndex: conflictIndex,
			ConflictTerm:  conflictTerm,
		})
	})

	r.POST("/rpc/pre-vote", func(c *gin.Context) {
		var request internal.PreVoteRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
			return
		}

		term, voteGranted := raft.HandlePreVote(
			request.Term,
			request.CandidateID,
			request.LastLogEntryIndex,
			request.LastLogEntryTerm,
		)

		c.JSON(http.StatusOK, internal.PreVoteResponse{
			Term:        term,
			VoteGranted: voteGranted,
		})
	})

	r.POST("/rpc/request-vote", func(c *gin.Context) {
		var request internal.RequestVoteRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
			return
		}

		term, voteGranted := raft.HandleRequestVote(
			request.Term,
			request.CandidateID,
			request.LastLogEntryIndex,
			request.LastLogEntryTerm,
		)

		c.JSON(http.StatusOK, internal.RequestVoteResponse{
			Term:        term,
			VoteGranted: voteGranted,
		})
	})

	r.POST("/rpc/propose", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)

		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
			return
		}

		result, err := raft.Propose(c, body)

		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})

			return
		}

		c.JSON(http.StatusOK, gin.H{
			"result": result,
		})
	})

	r.POST("/rpc/install-snapshot", func(c *gin.Context) {
		var request internal.InstallSnapshotRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
			return
		}

		term := raft.HandleInstallSnapshot(
			request.Term,
			request.LeaderId,
			request.LastIncludedIndex,
			request.LastIncludedTerm,
			request.Data,
		)

		c.JSON(http.StatusOK, internal.InstallSnapshotResponse{
			Term: term,
		})
	})

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

	r.POST("/propose", func(c *gin.Context) {
		result, err := raft.Propose(c, []byte(uuid.New().String()))

		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})

			return
		}

		c.JSON(http.StatusOK, gin.H{
			"result": result,
		})
	})

	r.POST("/snapshot", func(c *gin.Context) {
		if err := raft.TakeSnapshot(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})

			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "snapshot taken"})
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := r.Run(addr); err != nil && err != http.ErrServerClosed {
			// log.Fatalf("listen: %s\n", err)
		}
	}()

	go func() {
		ticker := time.NewTicker(350 * time.Millisecond)
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

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				raft.TakeSnapshot()
			case <-ctx.Done():
				return
			}
		}
	}()

	<-ctx.Done()

	log.Close()
}
