package internal

import (
	"context"
	"fmt"
	"sync/atomic"
)

type CandidateRole struct {
	raft             *Raft
	electionDeadline uint16
	electionTicks    uint16
}

func NewCandidateRole(raft *Raft) *CandidateRole {
	electionTimeoutMin := 15 * 5
	electionTimeoutMax := 15 * 6

	return &CandidateRole{
		raft:             raft,
		electionDeadline: uint16(electionTimeoutMin + raft.rng.IntN(electionTimeoutMax-electionTimeoutMin+1)),
		electionTicks:    0,
	}
}

func (c *CandidateRole) GetType() string { return "candidate" }

func (c *CandidateRole) OnEnter(_ uint64) {
	currentTerm := c.raft.store.IncrementCurrentTerm()

	c.raft.store.SetVotedFor(c.raft.id)

	id := c.raft.id

	nodes := append([]string(nil), c.raft.nodes...)

	numberOfVotes := uint32(1)

	if numberOfVotes > uint32(len(nodes)/2) {
		leaderRole := c.raft.becomeLeader()

		leaderRole.OnEnter(currentTerm)

		return
	}

	lastLogEntryIndex, lastLogEntryTerm := GetLastLogEntryIndexAndTerm(c.raft.log)

	for _, node := range nodes {
		if node == id {
			continue
		}

		go func(n string, t uint64, cid string, llei uint64, llet uint64, numberOfNodes int) {
			term, voteGranted := c.raft.transport.RequestVote(n, t, cid, llei, llet)

			currentTerm := c.raft.store.GetCurrentTerm()

			if term > currentTerm {
				followerRole := c.raft.becomeFollower()

				followerRole.OnEnter(term)

				return
			}

			if !voteGranted || term != t {
				return
			}

			if c.raft.role.GetType() != "candidate" || currentTerm != t {
				return
			}

			atomic.AddUint32(&numberOfVotes, 1)

			if numberOfVotes > uint32(numberOfNodes/2) {
				leaderRole := c.raft.becomeLeader()

				leaderRole.OnEnter(term)

				return
			}
		}(node, currentTerm, id, lastLogEntryIndex, lastLogEntryTerm, len(nodes))
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
