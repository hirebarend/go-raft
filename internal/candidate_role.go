package internal

import (
	"context"
	"fmt"
)

type CandidateRole struct {
	raft *Raft
}

func NewCandidateRole(raft *Raft) *CandidateRole {
	return &CandidateRole{raft: raft}
}

func (c *CandidateRole) GetType() string { return "candidate" }

func (c *CandidateRole) OnEnter(_ uint64) {
	c.raft.mu.Lock()

	currentTerm := c.raft.store.SetCurrentTerm(c.raft.store.GetCurrentTerm() + 1)

	numberOfVotes := 1

	c.raft.store.SetVotedFor(c.raft.id)

	c.raft.resetElectionTimer()

	nodes := append([]string(nil), c.raft.nodes...)

	id := c.raft.id

	if numberOfVotes > len(nodes)/2 {
		c.raft.role = NewLeaderRole(c.raft)

		c.raft.role.OnEnter(currentTerm)

		c.raft.mu.Unlock()

		return
	}

	lastLogEntryIndex, _ := c.raft.store.log.LastIndex()
	lastLogEntryTerm, _ := c.raft.store.log.LastTerm()

	c.raft.mu.Unlock()

	for _, node := range nodes {
		if node == id {
			continue
		}

		go func(n string, t uint64, cid string, llei uint64, llet uint64, numberOfNodes int) {
			term, voteGranted := c.raft.transport.RequestVote(n, t, cid, llei, llet)

			c.raft.mu.Lock()

			if term > c.raft.store.GetCurrentTerm() {
				c.raft.role = NewFollowerRole(c.raft)

				c.raft.mu.Unlock()

				c.raft.role.OnEnter(term)

				return
			}

			if !voteGranted || term != t {
				c.raft.mu.Unlock()

				return
			}

			if c.raft.role.GetType() != "candidate" || c.raft.store.GetCurrentTerm() != t {
				c.raft.mu.Unlock()

				return
			}

			numberOfVotes++

			if numberOfVotes > numberOfNodes/2 {
				c.raft.role = NewLeaderRole(c.raft)

				c.raft.mu.Unlock()

				c.raft.role.OnEnter(t)

				return
			}

			c.raft.mu.Unlock()
		}(node, currentTerm, id, lastLogEntryIndex, lastLogEntryTerm, len(nodes))
	}
}

func (c *CandidateRole) OnExit() {}

func (c *CandidateRole) Tick() {
	c.raft.mu.Lock()

	if c.raft.ticks >= c.raft.electionDeadline {
		c.raft.mu.Unlock()

		c.OnEnter(c.raft.store.GetCurrentTerm())

		return
	}

	c.raft.mu.Unlock()
}

func (c *CandidateRole) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommit uint64,
) (uint64, bool) {
	c.raft.mu.Lock()

	currentTerm := c.raft.store.GetCurrentTerm()

	if term < currentTerm {
		c.raft.mu.Unlock()

		return currentTerm, false
	}

	c.raft.role = NewFollowerRole(c.raft)

	c.raft.mu.Unlock()

	c.raft.role.OnEnter(term)

	return c.raft.role.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommit)
}

func (c *CandidateRole) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	c.raft.mu.Lock()
	defer c.raft.mu.Unlock()

	currentTerm := c.raft.store.GetCurrentTerm()

	if term <= currentTerm {
		return currentTerm, false
	}

	myLastLogEntry, ok := c.raft.store.log.Last()

	myLastLogEntryIndex := uint64(0)
	myLastLogEntryTerm := uint64(0)

	if ok {
		myLastLogEntryIndex = myLastLogEntry.Index
		myLastLogEntryTerm = myLastLogEntry.Term
	}

	if !logIsUpToDate(myLastLogEntryIndex, myLastLogEntryTerm, lastLogEntryIndex, lastLogEntryTerm) {
		return currentTerm, false
	}

	return currentTerm, true
}

func (c *CandidateRole) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	c.raft.mu.Lock()

	currentTerm := c.raft.store.GetCurrentTerm()

	if term <= currentTerm {
		return currentTerm, false
	}

	c.raft.role = NewFollowerRole(c.raft)

	c.raft.mu.Unlock()

	c.raft.role.OnEnter(term)

	return c.raft.role.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (c *CandidateRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	c.raft.mu.Lock()
	defer c.raft.mu.Unlock()

	return nil, fmt.Errorf("not leader")
}
