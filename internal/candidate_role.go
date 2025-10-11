package internal

import (
	"context"
	"fmt"
)

type CandidateRole struct {
	raft             *Raft
	electionDeadline int
	electionTicks    int
	majority         int
	nodes            []string
	votes            int
}

func NewCandidateRole(raft *Raft) *CandidateRole {
	electionTimeoutMin := 15 * 5
	electionTimeoutMax := 15 * 6

	return &CandidateRole{
		raft:             raft,
		electionDeadline: electionTimeoutMin + raft.rng.IntN(electionTimeoutMax-electionTimeoutMin+1),
		electionTicks:    0,
		majority:         len(raft.nodes)/2 + 1,
		nodes:            append([]string(nil), raft.nodes...),
		votes:            0,
	}
}

func (c *CandidateRole) GetType() string { return "candidate" }

func (c *CandidateRole) OnEnter(_ uint64) {
	currentTerm := c.raft.store.IncrementCurrentTerm()

	c.raft.store.SetVotedFor(c.raft.id)
	c.votes++

	if c.votes >= c.majority {
		c.raft.becomeLeader(currentTerm)

		return
	}

	lastLogEntryIndex, lastLogEntryTerm := GetLastLogEntryIndexAndTerm(c.raft.log)

	for _, node := range c.nodes {
		if node == c.raft.id {
			continue
		}

		go c.sendRequestVote(node, currentTerm, c.raft.id, lastLogEntryIndex, lastLogEntryTerm)
	}
}

func (c *CandidateRole) OnExit() {}

func (c *CandidateRole) Tick() {
	c.raft.mu.Lock()
	defer c.raft.mu.Unlock()

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
	leaderCommit uint64,
) (uint64, bool, uint64, uint64) {
	c.raft.mu.Lock()

	currentTerm := c.raft.store.GetCurrentTerm()

	if term < currentTerm {
		c.raft.mu.Unlock()

		return currentTerm, false, 0, 0
	}

	followerRole := c.raft.becomeFollower(currentTerm)

	c.raft.mu.Unlock()

	return followerRole.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommit)
}

func (c *CandidateRole) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	c.raft.mu.Lock()
	defer c.raft.mu.Unlock()

	currentTerm := c.raft.store.GetCurrentTerm()

	if term <= currentTerm {
		return currentTerm, false
	}

	myLastLogEntryIndex, myLastLogEntryTerm := GetLastLogEntryIndexAndTerm(c.raft.log)

	if !logIsUpToDate(myLastLogEntryIndex, myLastLogEntryTerm, lastLogEntryIndex, lastLogEntryTerm) {
		return currentTerm, false
	}

	return currentTerm, true
}

func (c *CandidateRole) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	c.raft.mu.Lock()

	currentTerm := c.raft.store.GetCurrentTerm()

	if term <= currentTerm {
		c.raft.mu.Unlock()

		return currentTerm, false
	}

	followerRole := c.raft.becomeFollower(term)

	c.raft.mu.Unlock()

	return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (c *CandidateRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	return nil, fmt.Errorf("not leader")
}

func (c *CandidateRole) sendRequestVote(node string, term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) {
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
