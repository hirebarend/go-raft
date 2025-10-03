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
		leaderRole := c.raft.becomeLeader()

		c.raft.mu.Unlock()

		leaderRole.OnEnter(currentTerm)

		return
	}

	lastLogEntryIndex, lastLogEntryTerm := GetLastLogEntryIndexAndTerm(c.raft.log)

	c.raft.mu.Unlock()

	for _, node := range nodes {
		if node == id {
			continue
		}

		go func(n string, t uint64, cid string, llei uint64, llet uint64, numberOfNodes int) {
			term, voteGranted := c.raft.transport.RequestVote(n, t, cid, llei, llet)

			fmt.Printf("[%v] c.raft.transport.RequestVote => %v, %v\n", n, term, voteGranted)

			c.raft.mu.Lock()

			if term > c.raft.store.GetCurrentTerm() {
				fmt.Printf("[%v] c.raft.transport.RequestVote => term is greater than current term\n", n)

				followerRole := c.raft.becomeFollower()

				c.raft.mu.Unlock()

				followerRole.OnEnter(term)

				return
			}

			if !voteGranted || term != t {
				fmt.Printf("[%v] c.raft.transport.RequestVote => vote not granted or term changed\n", n)

				c.raft.mu.Unlock()

				return
			}

			if c.raft.role.GetType() != "candidate" || c.raft.store.GetCurrentTerm() != t {
				fmt.Printf("[%v] c.raft.transport.RequestVote => no longer candidate (%v)\n", n, c.raft.role.GetType())

				c.raft.mu.Unlock()

				return
			}

			numberOfVotes++

			if numberOfVotes > numberOfNodes/2 {
				leaderRole := c.raft.becomeLeader()

				c.raft.mu.Unlock()

				leaderRole.OnEnter(term)

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
		currentTerm := c.raft.store.GetCurrentTerm()

		candidateRole := c.raft.becomeCandidate()

		c.raft.mu.Unlock()

		candidateRole.OnEnter(currentTerm)

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
) (uint64, bool, uint64, uint64) {
	c.raft.mu.Lock()

	currentTerm := c.raft.store.GetCurrentTerm()

	if term < currentTerm {
		c.raft.mu.Unlock()

		return currentTerm, false, 0, 0
	}

	followerRole := c.raft.becomeFollower()

	c.raft.mu.Unlock()

	followerRole.OnEnter(term)

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

	followerRole := c.raft.becomeFollower()

	c.raft.mu.Unlock()

	followerRole.OnEnter(term)

	return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (c *CandidateRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	c.raft.mu.Lock()
	defer c.raft.mu.Unlock()

	return nil, fmt.Errorf("not leader")
}
