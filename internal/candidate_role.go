package internal

import (
	"context"
	"errors"
	"fmt"
	"runtime"
)

type CandidateRole struct {
	raft             *Raft
	electionDeadline int
	electionTicks    int
	majority         int
	nodes            []string
	preVote          bool
	preVotes         int
	votes            int
}

func NewCandidateRole(raft *Raft) *CandidateRole {
	return &CandidateRole{
		raft:             raft,
		electionDeadline: raft.config.ElectionTimeoutMinTicks + raft.rng.IntN(raft.config.ElectionTimeoutMaxTicks-raft.config.ElectionTimeoutMinTicks+1),
		electionTicks:    0,
		majority:         len(raft.nodes)/2 + 1,
		nodes:            append([]string(nil), raft.nodes...),
		preVote:          true,
		preVotes:         0,
		votes:            0,
	}
}

func (c *CandidateRole) GetType() string { return "candidate" }

func (c *CandidateRole) OnEnter(_ uint64) {
	if c.preVote {
		c.startPreElection()

		return
	}

	c.startElection()
}

func (c *CandidateRole) OnExit() {}

func (c *CandidateRole) Tick() {
	c.electionTicks++

	if c.electionTicks >= c.electionDeadline {
		c.raft.becomeCandidate()

		return
	}
}

func (c *CandidateRole) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommitIndex uint64,
) (uint64, bool, uint64, uint64) {
	currentTerm := c.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false, 0, 0
	}

	followerRole := c.raft.becomeFollower(term)

	return followerRole.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommitIndex)
}

func (c *CandidateRole) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	currentTerm := c.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	if !c.raft.isLogEqualOrMoreRecent(lastLogEntryIndex, lastLogEntryTerm) {
		return currentTerm, false
	}

	return currentTerm, true
}

func (c *CandidateRole) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	currentTerm := c.raft.store.GetCurrentTerm()

	if term <= currentTerm {
		return currentTerm, false
	}

	followerRole := c.raft.becomeFollower(term)

	return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (c *CandidateRole) HandlePropose(ctx context.Context, data []byte, clientId string, sequenceNumber uint64, epoch uint64) (any, error) {
	leaderId := c.raft.GetLeaderId()

	if leaderId == "" {
		return nil, errors.New("no known leader")
	}

	return c.raft.transport.Propose(leaderId, data)
}

func (c *CandidateRole) HandleInstallSnapshot(term uint64, leaderId string, lastIncludedIndex uint64, lastIncludedTerm uint64, data []byte) uint64 {
	currentTerm := c.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm
	}

	followerRole := c.raft.becomeFollower(term)

	return followerRole.HandleInstallSnapshot(term, leaderId, lastIncludedIndex, lastIncludedTerm, data)
}

func (c *CandidateRole) startPreElection() {
	currentTerm := c.raft.store.GetCurrentTerm()

	c.preVotes++

	if c.preVotes >= c.majority {
		c.preVote = false

		c.startElection()

		return
	}

	lastLogEntryIndex, lastLogEntryTerm := c.raft.getLastLogEntryIndexAndTerm()

	for _, node := range c.nodes {
		if node == c.raft.id {
			continue
		}

		go c.sendPreVote(node, currentTerm, c.raft.id, lastLogEntryIndex, lastLogEntryTerm)
	}
}

func (c *CandidateRole) startElection() {
	currentTerm, err := c.raft.store.IncrementCurrentTerm()
	if err != nil {
		fmt.Printf("[%v] FATAL: failed to persist term increment: %v\n", c.raft.id, err)
		c.raft.markUnhealthy()
		return
	}

	if err := c.raft.store.SetVotedFor(c.raft.id); err != nil {
		fmt.Printf("[%v] FATAL: failed to persist votedFor: %v\n", c.raft.id, err)
		c.raft.markUnhealthy()
		return
	}
	c.votes++

	if c.votes >= c.majority {
		c.raft.becomeLeader(currentTerm)

		return
	}

	lastLogEntryIndex, lastLogEntryTerm := c.raft.getLastLogEntryIndexAndTerm()

	for _, node := range c.nodes {
		if node == c.raft.id {
			continue
		}

		go c.sendRequestVote(node, currentTerm, c.raft.id, lastLogEntryIndex, lastLogEntryTerm)
	}
}

func (c *CandidateRole) sendPreVote(node string, term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, false)
			fmt.Printf("[%v] PANIC in sendPreVote(%v): %v\n%s\n", c.raft.id, node, r, buf[:n])
		}
	}()
	t, granted := c.raft.transport.PreVote(node, term+1, candidateId, lastLogEntryIndex, lastLogEntryTerm)

	c.raft.mu.Lock()
	defer c.raft.mu.Unlock()

	currentTerm := c.raft.store.GetCurrentTerm()

	if t > currentTerm {
		c.raft.becomeFollower(t)

		return
	}

	if !granted {
		return
	}

	if c.raft.role.GetType() != "candidate" || !c.preVote || currentTerm != term {
		return
	}

	c.preVotes++

	if c.preVotes >= c.majority {
		c.preVote = false

		c.startElection()
	}
}

func (c *CandidateRole) sendRequestVote(node string, term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, false)
			fmt.Printf("[%v] PANIC in sendRequestVote(%v): %v\n%s\n", c.raft.id, node, r, buf[:n])
		}
	}()
	t, voteGranted := c.raft.transport.RequestVote(node, term, candidateId, lastLogEntryIndex, lastLogEntryTerm)

	c.raft.mu.Lock()
	defer c.raft.mu.Unlock()

	currentTerm := c.raft.store.GetCurrentTerm()

	if t > currentTerm {
		c.raft.becomeFollower(t)

		return
	}

	if !voteGranted || t != term {
		return
	}

	isCandidate := c.raft.role.GetType() == "candidate"

	if !isCandidate || currentTerm != term {
		return
	}

	c.votes++

	if c.votes >= c.majority {
		c.raft.becomeLeader(term)

		return
	}
}
