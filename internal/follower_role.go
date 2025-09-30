package internal

import (
	"context"
	"fmt"
)

type FollowerRole struct {
	raft *Raft
}

func NewFollowerRole(raft *Raft) *FollowerRole {
	return &FollowerRole{
		raft: raft,
	}
}

func (f *FollowerRole) GetType() string {
	return "follower"
}

func (f *FollowerRole) OnEnter(term uint64) {
	f.raft.mu.Lock()

	currentTerm := f.raft.store.GetCurrentTerm()

	if term > currentTerm {
		f.raft.store.SetCurrentTerm(term)
		f.raft.store.SetVotedFor("")
	}

	f.raft.resetElectionTimer()
	f.raft.leaderId = ""

	f.raft.mu.Unlock()
}

func (f *FollowerRole) OnExit() {
}

func (f *FollowerRole) Tick() {
	f.raft.mu.Lock()

	if f.raft.ticks >= f.raft.electionDeadline {
		currentTerm := f.raft.store.GetCurrentTerm()

		candidateRole := NewCandidateRole(f.raft)

		f.raft.role = candidateRole

		f.raft.mu.Unlock()

		candidateRole.OnEnter(currentTerm)

		return
	}

	f.raft.mu.Unlock()
}

func (f *FollowerRole) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommit uint64,
) (uint64, bool) {
	f.raft.mu.Lock()

	currentTerm := f.raft.store.GetCurrentTerm()

	if term < currentTerm {
		f.raft.mu.Unlock()

		return currentTerm, false
	}

	if term > currentTerm {
		followerRole := NewFollowerRole(f.raft)

		f.raft.role = followerRole

		f.raft.mu.Unlock()

		followerRole.OnEnter(term)

		return followerRole.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommit)
	}

	f.raft.leaderId = leaderId

	f.raft.resetElectionTimer()

	if !f.raft.isLogEntryOkay(prevLogEntryIndex, prevLogEntryTerm) {
		f.raft.mu.Unlock()

		return currentTerm, false
	}

	f.raft.appendEntriesLocked(prevLogEntryIndex, logEntries)

	f.raft.setCommitIndexLocked(leaderCommit)

	f.raft.mu.Unlock()

	return currentTerm, true
}

func (f *FollowerRole) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	f.raft.mu.Lock()
	defer f.raft.mu.Unlock()

	currentTerm := f.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	myLastLogEntry, ok := f.raft.store.log.Last()

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

func (f *FollowerRole) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	f.raft.mu.Lock()

	currentTerm := f.raft.store.GetCurrentTerm()

	if term < currentTerm {
		f.raft.mu.Unlock()

		return currentTerm, false
	}

	if term > currentTerm {
		followerRole := NewFollowerRole(f.raft)

		f.raft.role = followerRole

		f.raft.mu.Unlock()

		followerRole.OnEnter(term)

		return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
	}

	if f.raft.store.GetVotedFor() != "" && f.raft.store.GetVotedFor() != candidateId {
		f.raft.mu.Unlock()

		return currentTerm, false
	}

	myLastLogEntry, ok := f.raft.store.log.Last()

	myLastLogEntryIndex := uint64(0)
	myLastLogEntryTerm := uint64(0)

	if ok {
		myLastLogEntryIndex = myLastLogEntry.Index
		myLastLogEntryTerm = myLastLogEntry.Term
	}

	if !logIsUpToDate(myLastLogEntryIndex, myLastLogEntryTerm, lastLogEntryIndex, lastLogEntryTerm) {
		f.raft.mu.Unlock()

		return currentTerm, false
	}

	f.raft.store.SetVotedFor(candidateId)

	f.raft.resetElectionTimer()

	f.raft.mu.Unlock()

	return currentTerm, true
}

func (f *FollowerRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	f.raft.mu.Lock()
	defer f.raft.mu.Unlock()

	return nil, fmt.Errorf("not leader")
}
