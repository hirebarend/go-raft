package internal

import (
	"context"
	"fmt"
	"sync/atomic"
)

type CandidateRole struct {
	raft             *Raft
	electionDeadline int
	electionTicks    int
	votes            atomic.Int32
}

func NewCandidateRole(raft *Raft) *CandidateRole {
	electionTimeoutMin := 15 * 5
	electionTimeoutMax := 15 * 6

	return &CandidateRole{
		raft:             raft,
		electionDeadline: electionTimeoutMin + raft.rng.IntN(electionTimeoutMax-electionTimeoutMin+1),
		electionTicks:    0,
		// votes:            0,
	}
}

func (c *CandidateRole) GetType() string { return "candidate" }

func (c *CandidateRole) OnEnter(_ uint64) {
	currentTerm := c.raft.store.IncrementCurrentTerm()

	c.raft.store.SetVotedFor(c.raft.id)

	id := c.raft.id

	nodes := append([]string(nil), c.raft.nodes...)

	majority := len(c.raft.nodes)/2 + 1

	if int(c.votes.Add(1)) >= majority {
		leaderRole := c.raft.becomeLeader()

		leaderRole.OnEnter(currentTerm)

		return
	}

	lastLogEntryIndex, lastLogEntryTerm := GetLastLogEntryIndexAndTerm(c.raft.log)

	for _, node := range nodes {
		if node == id {
			continue
		}

		go c.sendRequestVote(node, currentTerm, id, lastLogEntryIndex, lastLogEntryTerm)
	}
}

func (c *CandidateRole) OnExit() {}

func (c *CandidateRole) Tick() {
	c.electionTicks++

	if c.electionTicks >= c.electionDeadline {
		currentTerm := c.raft.store.GetCurrentTerm()

		candidateRole := c.raft.becomeCandidate()

		candidateRole.OnEnter(currentTerm)

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
	currentTerm := c.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false, 0, 0
	}

	followerRole := c.raft.becomeFollower()

	followerRole.OnEnter(term)

	return followerRole.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommit)
}

func (c *CandidateRole) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
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
	currentTerm := c.raft.store.GetCurrentTerm()

	if term <= currentTerm {
		return currentTerm, false
	}

	followerRole := c.raft.becomeFollower()

	followerRole.OnEnter(term)

	return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (c *CandidateRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	return nil, fmt.Errorf("not leader")
}

func (c *CandidateRole) sendRequestVote(node string, term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) {
	t, voteGranted := c.raft.transport.RequestVote(node, term, candidateId, lastLogEntryIndex, lastLogEntryTerm)

	currentTerm := c.raft.store.GetCurrentTerm()

	if t > currentTerm {
		followerRole := c.raft.becomeFollower()

		followerRole.OnEnter(t)

		return
	}

	if !voteGranted || t != term {
		return
	}

	if c.raft.role.GetType() != "candidate" || currentTerm != term {
		return
	}

	majority := len(c.raft.nodes)/2 + 1

	if int(c.votes.Add(1)) >= majority {
		leaderRole := c.raft.becomeLeader()

		leaderRole.OnEnter(term)

		return
	}
}
