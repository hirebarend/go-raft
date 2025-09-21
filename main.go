package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hirebarend/raft-go/internal"
)

func main() {
	if os.Getenv("ENV") == "PRODUCTION" {
		gin.SetMode(gin.ReleaseMode)
	}

	nodes := flag.String("nodes", "127.0.0.1:8081,127.0.0.1:8082,127.0.0.1:8083", "Port number to run the service on")
	port := flag.Int("port", 8081, "Port number to run the service on")

	flag.Parse()

	addr := fmt.Sprintf("127.0.0.1:%d", *port)

	store := internal.NewStore(&internal.Log{})
	raft := internal.NewRaft(addr, strings.Split(*nodes, ","), store, &internal.Transport{}, &internal.FSM{})

	r := gin.Default()

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})

	r.POST("/append-entries", func(c *gin.Context) {
		var request internal.AppendEntriesRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
			return
		}

		term, success := raft.HandleAppendEntries(
			request.Term, request.LeaderId, request.PrevLogIndex, request.PrevLogTerm, request.Entries, request.LeaderCommit,
		)

		c.JSON(http.StatusOK, internal.AppendEntriesResponse{
			Term:    term,
			Success: success,
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go raft.StartApplier()

	go func() {
		ticker := time.NewTicker(1000 * time.Millisecond)
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

	// <>
	go func() {
		ticker := time.NewTicker(3000 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				_, err := raft.Propose(ctx, []byte("hello world"))
				if err == nil {
					fmt.Printf("[%v] APPLIED\n", addr)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	// </>

	go func() {
		if err := r.Run(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	<-ctx.Done()
}
