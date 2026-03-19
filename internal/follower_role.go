package internal

import (
	"context"
	"errors"
)

type FollowerRole struct {
	raft             *Raft
	electionDeadline int
	electionTicks    int
}

func NewFollowerRole(raft *Raft) *FollowerRole {
	electionTimeoutMin := 4 * 3
	electionTimeoutMax := 4 * 5

	return &FollowerRole{
		raft:             raft,
		electionDeadline: electionTimeoutMin + raft.rng.IntN(electionTimeoutMax-electionTimeoutMin+1),
		electionTicks:    0,
	}
}

func (f *FollowerRole) GetType() string {
	return "follower"
}

func (f *FollowerRole) OnEnter(term uint64) {
	currentTerm := f.raft.store.GetCurrentTerm()

	if term > currentTerm {
		f.raft.store.SetCurrentTermAndVotedFor(term, "")
	}

	f.raft.store.SetLeaderId("")
}

func (f *FollowerRole) OnExit() {
}

func (f *FollowerRole) Tick() {
	f.electionTicks++

	if f.electionTicks >= f.electionDeadline {
		f.raft.becomeCandidate()

		return
	}
}

func (f *FollowerRole) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommitIndex uint64,
) (uint64, bool, uint64, uint64) {
	currentTerm := f.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false, 0, 0
	}

	if term > currentTerm {
		followerRole := f.raft.becomeFollower(term)

		return followerRole.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommitIndex)
	}

	f.resetElectionTimer()

	lastLogEntryIndex, _ := f.raft.log.GetLastIndex()

	if prevLogEntryIndex > lastLogEntryIndex {
		return currentTerm, false, lastLogEntryIndex + 1, 0
	}

	ok, conflictIndex, conflictTerm := f.checkLogConsistency(prevLogEntryIndex, prevLogEntryTerm)

	if !ok {
		return currentTerm, false, conflictIndex, conflictTerm
	}

	f.raft.store.SetLeaderId(leaderId)

	f.appendEntries(prevLogEntryIndex, logEntries)

	f.raft.setCommitIndex(leaderCommitIndex)

	f.applyToFiniteStateMachine()

	return currentTerm, true, 0, 0
}

func (f *FollowerRole) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	currentTerm := f.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	if !f.raft.isLogEqualOrMoreRecent(lastLogEntryIndex, lastLogEntryTerm) {
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
		followerRole := f.raft.becomeFollower(term)

		return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
	}

	votedFor := f.raft.store.GetVotedFor()

	if votedFor != "" && votedFor != candidateId {
		return currentTerm, false
	}

	if !f.raft.isLogEqualOrMoreRecent(lastLogEntryIndex, lastLogEntryTerm) {
		return currentTerm, false
	}

	f.raft.store.SetVotedFor(candidateId)

	f.resetElectionTimer()

	return currentTerm, true
}

func (f *FollowerRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	leaderId := f.raft.GetLeaderId()

	if leaderId == "" {
		return nil, errors.New("no known leader")
	}

	return f.raft.transport.Propose(leaderId, data)
}

func (f *FollowerRole) HandleInstallSnapshot(term uint64, leaderId string, lastIncludedIndex uint64, lastIncludedTerm uint64, data []byte) uint64 {
	currentTerm := f.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm
	}

	if term > currentTerm {
		f.raft.store.SetCurrentTermAndVotedFor(term, "")
	}

	f.resetElectionTimer()

	f.raft.store.SetLeaderId(leaderId)

	if lastIncludedIndex <= f.raft.snapshotIndex {
		return currentTerm
	}

	if err := f.raft.installSnapshot(lastIncludedIndex, lastIncludedTerm, data); err != nil {
		return currentTerm
	}

	return currentTerm
}

func (f *FollowerRole) appendEntries(prevLogEntryIndex uint64, logEntries []LogEntry) {
	for i, entry := range logEntries {
		idx := prevLogEntryIndex + 1 + uint64(i)

		raw, err := f.raft.log.Read(idx)

		if err == nil {
			logEntryAtIdx, err := DeserializeLogEntry(raw)
			if err != nil {
				return
			}
			if logEntryAtIdx.Term != entry.Term {
				err := f.raft.log.TruncateFrom(idx)

				if err == nil {
					if f.writeEntries(logEntries[i:]) {
						f.raft.log.Commit()
					}
				}

				return
			}

			continue
		}

		if f.writeEntries(logEntries[i:]) {
			f.raft.log.Commit()
		}

		return
	}
}

func (f *FollowerRole) writeEntries(entries []LogEntry) bool {
	for _, logEntry := range entries {
		_, err := f.raft.log.Write(logEntry.Serialize())

		if err != nil {
			return false
		}
	}

	return true
}

func (f *FollowerRole) applyToFiniteStateMachine() {
	commitIndex := f.raft.store.commitIndex.Load()
	lastApplied := f.raft.store.lastApplied.Load()

	if commitIndex <= lastApplied {
		return
	}

	for idx := lastApplied + 1; idx <= commitIndex; idx++ {
		data, err := f.raft.log.Read(idx)

		if err != nil {
			break
		}

		logEntry, err := DeserializeLogEntry(data)

		if err != nil {
			break
		}

		if len(logEntry.Data) > 0 {
			f.raft.fsm.Apply(logEntry.Data)
		}

		f.raft.store.lastApplied.Store(idx)
	}
}

func (f *FollowerRole) checkLogConsistency(index, term uint64) (bool, uint64, uint64) {
	if index == 0 {
		return true, 0, 0
	}

	if index == f.raft.snapshotIndex {
		if term == f.raft.snapshotTerm {
			return true, 0, 0
		}

		return false, f.raft.snapshotIndex + 1, 0
	}

	if index < f.raft.snapshotIndex {
		return false, f.raft.snapshotIndex + 1, 0
	}

	raw, err := f.raft.log.Read(index)

	if err != nil {
		return false, index, 0
	}

	logEntryAtIndex, err := DeserializeLogEntry(raw)

	if err != nil || logEntryAtIndex == nil {
		return false, index, 0
	}

	if logEntryAtIndex.Term == term {
		return true, 0, 0
	}

	conflictTerm := logEntryAtIndex.Term
	conflictIndex := index

	for conflictIndex > 1 && conflictIndex > f.raft.snapshotIndex+1 {
		rawPrev, err := f.raft.log.Read(conflictIndex - 1)

		if err != nil {
			break
		}

		logEntry, err := DeserializeLogEntry(rawPrev)

		if err != nil || logEntry == nil || logEntry.Term != conflictTerm {
			break
		}

		conflictIndex--
	}

	return false, conflictIndex, conflictTerm
}

func (f *FollowerRole) resetElectionTimer() {
	electionTimeoutMin := 4 * 3
	electionTimeoutMax := 4 * 5

	f.electionDeadline = electionTimeoutMin + f.raft.rng.IntN(electionTimeoutMax-electionTimeoutMin+1)
	f.electionTicks = 0
}
