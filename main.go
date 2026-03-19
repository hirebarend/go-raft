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
	bindHost := flag.String("bind-host", "127.0.0.1", "Host/IP on which the HTTP server listens.")
	nodes := flag.String("nodes", "127.0.0.1:8081,127.0.0.1:8082,127.0.0.1:8083", "Comma-separated list of cluster peer addresses (host:port).")
	advertiseHost := flag.String("advertise-host", "", "Host/IP this node advertises to peers; defaults to bind-host.")
	port := flag.Int("port", 8081, "Port number on which this Raft node will listen for client and peer requests.")

	flag.Parse()

	effectiveAdvertiseHost := strings.TrimSpace(*advertiseHost)

	if effectiveAdvertiseHost == "" {
		effectiveAdvertiseHost = *bindHost
	}

	bindAddr := fmt.Sprintf("%s:%d", *bindHost, *port)
	advertiseAddr := fmt.Sprintf("%s:%d", effectiveAdvertiseHost, *port)

	log := golog.NewLog(*data, 64<<20)
	err := log.Load()

	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: failed to load write-ahead log from %q: %v\n", *data, err)
		os.Exit(1)
	}

	store, err := internal.NewStore(filepath.Join(*data, "store.data"))

	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: failed to load persistent state from %q: %v\n", *data, err)
		os.Exit(1)
	}

	config := internal.DefaultConfig()
	raft := internal.NewRaft(advertiseAddr, *data, strings.Split(*nodes, ","), log, store, internal.NewTransport(), internal.NewFSM(), config)

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

	srv := &http.Server{
		Addr:    bindAddr,
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "listen error: %v\n", err)
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

	fmt.Println("shutting down...")

	// Step down and drain pending proposals
	raft.Shutdown()

	// Gracefully shut down HTTP server (wait up to 5s for in-flight requests)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "http server shutdown error: %v\n", err)
	}

	log.Close()
}
