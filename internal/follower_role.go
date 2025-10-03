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

		candidateRole := f.raft.becomeCandidate()

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
) (uint64, bool, uint64, uint64) {
	f.raft.mu.Lock()

	currentTerm := f.raft.store.GetCurrentTerm()

	if term < currentTerm {
		f.raft.mu.Unlock()

		return currentTerm, false, 0, 0
	}

	if term > currentTerm {
		followerRole := f.raft.becomeFollower()

		f.raft.mu.Unlock()

		followerRole.OnEnter(term)

		return followerRole.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommit)
	}

	f.raft.leaderId = leaderId

	f.raft.resetElectionTimer()

	lastIndex, _ := f.raft.log.GetLastIndex()

	if prevLogEntryIndex > lastIndex {
		f.raft.mu.Unlock()

		return currentTerm, false, lastIndex + 1, 0
	}

	if prevLogEntryIndex > 0 {
		prev, err := f.raft.log.ReadAndDeserialize(prevLogEntryIndex)

		if err != nil || prev == nil {
			f.raft.mu.Unlock()

			return currentTerm, false, prevLogEntryIndex, 0
		}

		if prev.Term != prevLogEntryTerm {
			conflictTerm := prev.Term

			conflictIndex := prevLogEntryIndex

			for conflictIndex > 1 {
				e, err := f.raft.log.ReadAndDeserialize(conflictIndex - 1)

				if err != nil || e == nil || e.Term != conflictTerm {
					break
				}

				conflictIndex--
			}

			f.raft.mu.Unlock()

			return currentTerm, false, conflictIndex, conflictTerm
		}
	}

	f.raft.appendEntriesLocked(prevLogEntryIndex, logEntries)

	f.raft.setCommitIndexLocked(leaderCommit)

	f.raft.mu.Unlock()

	return currentTerm, true, 0, 0
}

func (f *FollowerRole) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	f.raft.mu.Lock()
	defer f.raft.mu.Unlock()

	currentTerm := f.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	myLastLogEntryIndex, myLastLogEntryTerm := GetLastLogEntryIndexAndTerm(f.raft.log)

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
		followerRole := f.raft.becomeFollower()

		f.raft.mu.Unlock()

		followerRole.OnEnter(term)

		return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
	}

	if f.raft.store.GetVotedFor() != "" && f.raft.store.GetVotedFor() != candidateId {
		f.raft.mu.Unlock()

		return currentTerm, false
	}

	myLastLogEntryIndex, myLastLogEntryTerm := GetLastLogEntryIndexAndTerm(f.raft.log)

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
