package internal

import (
	"context"
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
		electionDeadline: electionTimeoutMin + raft.rng.IntN(electionTimeoutMax-electionTimeoutMin+1),
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

	if term <= currentTerm {
		return currentTerm, false
	}

	if !IsEqualOrMoreRecent(c.raft.log, lastLogEntryIndex, lastLogEntryTerm) {
		return currentTerm, false
	}

	return currentTerm, true
}

func (c *CandidateRole) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	currentTerm := c.raft.store.GetCurrentTerm()

	// If the term of the candidate is less than or equal to the current term, reject the request.
	if term <= currentTerm {
		return currentTerm, false
	}

	// If the term of the candidate is greater than the current term, become a follower and process the request.
	followerRole := c.raft.becomeFollower(term)
	return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (c *CandidateRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	return c.raft.transport.Propose(c.raft.GetLeaderId(), data)
}

func (c *CandidateRole) startPreElection() {
	currentTerm := c.raft.store.GetCurrentTerm()

	c.votes++

	lastLogEntryIndex, lastLogEntryTerm := GetLastLogEntryIndexAndTerm(c.raft.log)

	for _, node := range c.nodes {
		if node == c.raft.id {
			continue
		}

		go c.sendPreVote(node, currentTerm, c.raft.id, lastLogEntryIndex, lastLogEntryTerm)
	}
}

func (c *CandidateRole) startElection() {
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

func (c *CandidateRole) sendPreVote(node string, term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) {
	t, granted := c.raft.transport.PreVote(node, term, candidateId, lastLogEntryIndex, lastLogEntryTerm)

	c.raft.mu.Lock()
	defer c.raft.mu.Unlock()

	// If the term has changed, we are no longer in the same election.
	if c.raft.store.GetCurrentTerm() != term {
		return
	}

	currentTerm := c.raft.store.GetCurrentTerm()

	if t > currentTerm {
		c.raft.becomeFollower(t)

		return
	}

	if !granted || t != term {
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
	t, voteGranted := c.raft.transport.RequestVote(node, term, candidateId, lastLogEntryIndex, lastLogEntryTerm)

	c.raft.mu.Lock()
	defer c.raft.mu.Unlock()

	// If the term has changed, we are no longer in the same election.
	if c.raft.store.GetCurrentTerm() != term {
		return
	}

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
