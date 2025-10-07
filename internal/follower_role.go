package internal

import (
	"context"
	"fmt"
	"sync"
)

type FollowerRole struct {
	raft             *Raft
	electionDeadline int
	electionTicks    int
	mu               *sync.Mutex
}

func NewFollowerRole(raft *Raft) *FollowerRole {
	electionTimeoutMin := 15 * 5
	electionTimeoutMax := 15 * 6

	return &FollowerRole{
		raft:             raft,
		electionDeadline: electionTimeoutMin + raft.rng.IntN(electionTimeoutMax-electionTimeoutMin+1),
		electionTicks:    0,
		mu:               &sync.Mutex{},
	}
}

func (f *FollowerRole) GetType() string {
	return "follower"
}

func (f *FollowerRole) OnEnter(term uint64) {
	currentTerm := f.raft.store.GetCurrentTerm()

	if term > currentTerm {
		f.raft.store.SetCurrentTerm(term)

		f.raft.store.SetVotedFor("")
	}

	f.raft.store.SetLeaderId("")
}

func (f *FollowerRole) OnExit() {
}

func (f *FollowerRole) Tick() {
	f.electionTicks++

	if f.electionTicks >= f.electionDeadline {
		currentTerm := f.raft.store.GetCurrentTerm()

		candidateRole := f.raft.becomeCandidate()

		candidateRole.OnEnter(currentTerm)

		return
	}
}

func (f *FollowerRole) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommit uint64,
) (uint64, bool, uint64, uint64) {
	currentTerm := f.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false, 0, 0
	}

	if term > currentTerm {
		followerRole := f.raft.becomeFollower()

		followerRole.OnEnter(term)

		return followerRole.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommit)
	}

	lastLogEntryIndex, _ := f.raft.log.GetLastIndex()

	if prevLogEntryIndex > lastLogEntryIndex {
		return currentTerm, false, lastLogEntryIndex + 1, 0
	}

	if prevLogEntryIndex > 0 {
		prevLogEntry, err := f.raft.log.ReadAndDeserialize(prevLogEntryIndex)

		if err != nil || prevLogEntry == nil {
			return currentTerm, false, prevLogEntryIndex, 0
		}

		if prevLogEntry.Term != prevLogEntryTerm {
			conflictTerm := prevLogEntry.Term

			conflictIndex := prevLogEntryIndex

			for conflictIndex > 1 {
				e, err := f.raft.log.ReadAndDeserialize(conflictIndex - 1)

				if err != nil || e == nil || e.Term != conflictTerm {
					break
				}

				conflictIndex--
			}

			return currentTerm, false, conflictIndex, conflictTerm
		}
	}

	f.resetElectionTimer()

	f.raft.store.SetLeaderId(leaderId)

	f.appendEntries(prevLogEntryIndex, logEntries)

	f.raft.setCommitIndex(leaderCommit)

	f.applyToFiniteStateMachine()

	return currentTerm, true, 0, 0
}

func (f *FollowerRole) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
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
	currentTerm := f.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	if term > currentTerm {
		followerRole := f.raft.becomeFollower()

		followerRole.OnEnter(term)

		return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
	}

	if f.raft.store.GetVotedFor() != "" && f.raft.store.GetVotedFor() != candidateId {
		return currentTerm, false
	}

	myLastLogEntryIndex, myLastLogEntryTerm := GetLastLogEntryIndexAndTerm(f.raft.log)

	if !logIsUpToDate(myLastLogEntryIndex, myLastLogEntryTerm, lastLogEntryIndex, lastLogEntryTerm) {
		return currentTerm, false
	}

	f.resetElectionTimer()

	f.raft.store.SetVotedFor(candidateId)

	return currentTerm, true
}

func (f *FollowerRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	return nil, fmt.Errorf("not leader")
}

func (f *FollowerRole) appendEntries(prevLogEntryIndex uint64, logEntries []LogEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i, entry := range logEntries {
		idx := prevLogEntryIndex + 1 + uint64(i)

		logEntryAtIdx, err := f.raft.log.ReadAndDeserialize(idx)

		if err == nil {
			if logEntryAtIdx.Term != entry.Term {
				err := f.raft.log.TruncateFrom(idx)

				if err == nil {
					for _, logEntry := range logEntries[i:] {
						_, err := f.raft.log.SerializeAndWrite(&logEntry)

						if err != nil {
							break
						}
					}

					f.raft.log.Commit()
				}

				return
			}

			continue
		}

		for _, logEntry := range logEntries[i:] {
			_, err := f.raft.log.SerializeAndWrite(&logEntry)

			if err != nil {
				break
			}
		}

		f.raft.log.Commit()

		return
	}
}

func (f *FollowerRole) applyToFiniteStateMachine() {
	// l.raft.applyMu.Lock()
	// defer l.raft.applyMu.Unlock()

	commitIndex := f.raft.store.commitIndex.Load()
	lastApplied := f.raft.store.lastApplied

	if commitIndex <= lastApplied {
		return
	}

	for idx := lastApplied + 1; idx <= commitIndex; idx++ {
		entry, err := f.raft.log.ReadAndDeserialize(idx)

		if err != nil {
			break
		}

		if len(entry.Data) > 0 {
			f.raft.fsm.Apply(entry.Data)
		}

		f.raft.store.lastApplied = idx
	}
}

func (f *FollowerRole) resetElectionTimer() {
	electionTimeoutMin := 15 * 5
	electionTimeoutMax := 15 * 6

	f.electionDeadline = electionTimeoutMin + f.raft.rng.IntN(electionTimeoutMax-electionTimeoutMin+1)
	f.electionTicks = 0
}
