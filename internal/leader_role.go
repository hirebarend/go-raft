package internal

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
)

const maxBatch = 2 * 1024

type LeaderRole struct {
	raft              *Raft
	applierCh         chan struct{}
	applierStopCh     chan struct{}
	heartbeatDeadline int
	heartbeatTicks    int
	inflight          map[string]bool
	matchIndex        map[string]uint64
	nextIndex         map[string]uint64
	pending           map[uint64][]chan any
}

func NewLeaderRole(raft *Raft) *LeaderRole {
	return &LeaderRole{
		raft:              raft,
		applierCh:         make(chan struct{}, 1),
		applierStopCh:     make(chan struct{}),
		heartbeatDeadline: 4,
		heartbeatTicks:    0,
		inflight:          make(map[string]bool),
		matchIndex:        make(map[string]uint64),
		nextIndex:         make(map[string]uint64),
		pending:           make(map[uint64][]chan any),
	}
}

func (l *LeaderRole) GetType() string {
	return "leader"
}

func (l *LeaderRole) OnEnter(term uint64) {
	currentTerm := l.raft.store.GetCurrentTerm()

	if currentTerm != term {
		l.raft.becomeFollower(term)

		return
	}

	l.raft.store.SetLeaderId(l.raft.id)

	lastLogEntryIndex, _ := l.raft.log.GetLastIndex()

	for _, p := range l.raft.nodes {
		if p == l.raft.id {
			continue
		}

		l.matchIndex[p] = 0
		l.nextIndex[p] = lastLogEntryIndex + 1
	}

	l.matchIndex[l.raft.id] = lastLogEntryIndex
	l.nextIndex[l.raft.id] = lastLogEntryIndex + 1

	logEntry := LogEntry{
		Data: nil,
		Term: term,
	}

	_, err := l.raft.log.Write(logEntry.Serialize())

	if err != nil {
		l.raft.becomeFollower(term)

		return
	}

	l.raft.log.Commit()

	lastLogEntryIndex, err = l.raft.log.GetLastIndex()

	if err != nil {
		l.raft.becomeFollower(term)

		return
	}

	l.matchIndex[l.raft.id] = lastLogEntryIndex
	l.nextIndex[l.raft.id] = lastLogEntryIndex + 1

	l.sendAppendEntriesToAllNodes()

	l.tryToAdvanceCommitIndex()

	go l.applier()
}

func (l *LeaderRole) OnExit() {
	l.stopApplier()

	for idx, waiters := range l.pending {
		for _, ch := range waiters {
			close(ch)
		}

		delete(l.pending, idx)
	}
}

func (l *LeaderRole) Tick() {
	l.heartbeatTicks++

	if l.heartbeatTicks >= l.heartbeatDeadline {
		l.sendAppendEntriesToAllNodes()
	}
}

func (l *LeaderRole) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommit uint64,
) (uint64, bool, uint64, uint64) {
	currentTerm := l.raft.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false, 0, 0
	}

	if term == currentTerm && leaderId == l.raft.id {
		return currentTerm, false, 0, 0
	}

	followerRole := l.raft.becomeFollower(term)

	return followerRole.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommit)
}

func (l *LeaderRole) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	currentTerm := l.raft.store.GetCurrentTerm()

	return currentTerm, false
}

func (l *LeaderRole) HandleInstallSnapshot(term uint64, leaderId string, lastIncludedIndex uint64, lastIncludedTerm uint64, data []byte) uint64 {
	currentTerm := l.raft.store.GetCurrentTerm()

	if term <= currentTerm {
		return currentTerm
	}

	followerRole := l.raft.becomeFollower(term)

	return followerRole.HandleInstallSnapshot(term, leaderId, lastIncludedIndex, lastIncludedTerm, data)
}

func (l *LeaderRole) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	currentTerm := l.raft.store.GetCurrentTerm()

	if term <= currentTerm {
		return currentTerm, false
	}

	followerRole := l.raft.becomeFollower(term)

	return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (l *LeaderRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	currentTerm := l.raft.store.GetCurrentTerm()

	logEntry := LogEntry{
		Data: data,
		Term: currentTerm,
	}

	index, err := l.raft.log.WriteCommit(logEntry.Serialize())

	if err != nil {
		return nil, err
	}

	ch := make(chan any, 1)

	l.raft.mu.Lock()

	l.pending[index] = append(l.pending[index], ch)

	l.sendAppendEntriesToAllNodes()

	if l.matchIndex[l.raft.id] < index {
		l.matchIndex[l.raft.id] = index
	}

	if l.nextIndex[l.raft.id] < index+1 {
		l.nextIndex[l.raft.id] = index + 1
	}

	l.raft.mu.Unlock()

	select {
	case res, ok := <-ch:
		if !ok {
			return nil, errors.New("leader stepped down")
		}

		return res, nil
	case <-ctx.Done():
		l.raft.mu.Lock()
		ws := l.pending[index]
		if len(ws) > 0 {
			for i := range ws {
				if ws[i] == ch {
					ws[i] = ws[len(ws)-1]
					ws = ws[:len(ws)-1]
					break
				}
			}
			if len(ws) > 0 {
				l.pending[index] = ws
			} else {
				delete(l.pending, index)
			}
		}
		l.raft.mu.Unlock()

		return nil, ctx.Err()
	}
}

func (l *LeaderRole) applier() {
	for {
		select {
		case <-l.applierCh:
			for {
				l.applyToFiniteStateMachine()

				select {
				case <-l.applierCh:
					continue
				default:
				}
				break
			}
		case <-l.applierStopCh:
			return
		}
	}
}

func (l *LeaderRole) stopApplier() { close(l.applierStopCh) }

func (l *LeaderRole) triggerApplier() {
	select {
	case l.applierCh <- struct{}{}:
	default:
	}
}

func (l *LeaderRole) applyToFiniteStateMachine() {
	commitIndex := l.raft.store.commitIndex.Load()
	lastApplied := l.raft.store.lastApplied.Load()

	if commitIndex <= lastApplied {
		return
	}

	for idx := lastApplied + 1; idx <= commitIndex; idx++ {
		raw, err := l.raft.log.Read(idx)

		if err != nil {
			break
		}

		logEntry, err := DeserializeLogEntry(raw)

		if err != nil {
			break
		}

		var result any

		if len(logEntry.Data) > 0 {
			result = l.raft.fsm.Apply(logEntry.Data)
		}

		l.raft.mu.Lock()
		waiters := l.pending[idx]

		if len(waiters) > 0 {
			delete(l.pending, idx)
		}
		l.raft.mu.Unlock()

		for _, ch := range waiters {
			if result == nil && len(logEntry.Data) > 0 {
				result = struct{}{}
			}
			ch <- result

			close(ch)
		}

		l.raft.store.lastApplied.Store(idx)
	}
}

func (l *LeaderRole) tryToAdvanceCommitIndex() {
	currentTerm := l.raft.store.GetCurrentTerm()

	matches := make([]uint64, 0, len(l.raft.nodes))

	for _, n := range l.raft.nodes {
		matches = append(matches, l.matchIndex[n])
	}

	if len(matches) == 0 {
		return
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i] < matches[j] })

	idx := len(matches) - (len(matches) / 2) - 1

	if idx < 0 || idx >= len(matches) {
		return
	}

	candidate := matches[idx]

	if candidate == 0 {
		return
	}

	raw, err := l.raft.log.Read(candidate)

	if err != nil {
		return
	}

	logEntry, err := DeserializeLogEntry(raw)

	if err != nil {
		return
	}

	if logEntry.Term != currentTerm {
		return
	}

	if candidate > l.raft.store.commitIndex.Load() {
		l.raft.setCommitIndex(candidate)

		l.triggerApplier()
	}
}

func (l *LeaderRole) resetHeartbeatTimer() {
	l.heartbeatTicks = 0
}

func (l *LeaderRole) sendAppendEntriesToAllNodes() {
	l.resetHeartbeatTimer()

	currentTerm := l.raft.store.GetCurrentTerm()

	lastLogEntryIndex, _ := l.raft.log.GetLastIndex()

	for _, node := range l.raft.nodes {
		if node == l.raft.id {
			continue
		}

		if l.inflight[node] {
			continue
		}

		l.inflight[node] = true

		next := l.nextIndex[node]

		if l.nextIndex[node] == 0 {
			next = lastLogEntryIndex + 1
		}

		if l.raft.snapshotIndex > 0 && next <= l.raft.snapshotIndex {
			go l.sendInstallSnapshotToNode(node, currentTerm, l.raft.id)

			continue
		}

		var prevLogEntryIndex uint64

		if next > 0 {
			prevLogEntryIndex = next - 1
		}

		var prevLogEntryTerm uint64

		if prevLogEntryIndex > 0 {
			if prevLogEntryIndex == l.raft.snapshotIndex {
				prevLogEntryTerm = l.raft.snapshotTerm
			} else {
				raw, err := l.raft.log.Read(prevLogEntryIndex)

				if err == nil {
					if logEntry, err := DeserializeLogEntry(raw); err == nil {
						prevLogEntryTerm = logEntry.Term
					}
				}
			}
		}

		var logEntries []LogEntry

		if next <= lastLogEntryIndex {
			to := lastLogEntryIndex
			count := to - next + 1

			if count > maxBatch {
				to = next + maxBatch - 1
			}

			logEntries = make([]LogEntry, 0, to-next+1)

			for i := next; i <= to; i++ {
				raw, err := l.raft.log.Read(i)

				if err == nil {
					logEntry, err := DeserializeLogEntry(raw)
					if err == nil {
						logEntries = append(logEntries, *logEntry)
					} else {
						break
					}
				} else {
					break
				}
			}
		}

		go l.sendAppendEntriesToNode(node, currentTerm, l.raft.GetLeaderId(), prevLogEntryIndex, prevLogEntryTerm, logEntries, l.raft.store.commitIndex.Load())
	}
}

func (l *LeaderRole) sendAppendEntriesToNode(node string, term uint64, leaderId string, prevLogEntryIndex uint64, prevLogEntryTerm uint64, logEntries []LogEntry, leaderCommitIndex uint64) {
	t, success, conflictIndex, conflictTerm := l.raft.transport.AppendEntries(node, term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommitIndex)

	l.raft.mu.Lock()
	defer l.raft.mu.Unlock()

	if l.inflight[node] {
		l.inflight[node] = false
	}

	if t > l.raft.store.GetCurrentTerm() {
		l.raft.becomeFollower(t)

		return
	}

	if l.raft.role.GetType() != "leader" || t != term {
		return
	}

	needsRetry := false

	if success {
		lastSent := prevLogEntryIndex

		if len(logEntries) > 0 {
			lastSent = prevLogEntryIndex + uint64(len(logEntries))
		}

		if l.matchIndex[node] < lastSent {
			l.matchIndex[node] = lastSent
		}

		next := lastSent + 1

		if l.nextIndex[node] < next {
			l.nextIndex[node] = next
		}

		l.tryToAdvanceCommitIndex()

		lastLogEntryIndex, _ := l.raft.log.GetLastIndex()

		if l.nextIndex[node] <= lastLogEntryIndex {
			needsRetry = true
		}
	} else {
		if l.nextIndex[node] > 1 {
			if conflictTerm != 0 {
				lastOfTerm := GetLastLogEntryIndexOfTerm(l.raft.log, conflictTerm)

				if lastOfTerm > 0 {
					l.nextIndex[node] = lastOfTerm + 1
				} else {
					l.nextIndex[node] = conflictIndex
				}
			} else {
				l.nextIndex[node] = conflictIndex
			}

			if l.nextIndex[node] < 1 {
				l.nextIndex[node] = 1
			}

			needsRetry = true
		}
	}

	if needsRetry {
		l.retrySendAppendEntriesToNode(node, term)
	}
}

func (l *LeaderRole) retrySendAppendEntriesToNode(node string, term uint64) {
	if l.inflight[node] {
		return
	}

	l.inflight[node] = true

	next := l.nextIndex[node]

	if l.raft.snapshotIndex > 0 && next <= l.raft.snapshotIndex {
		go l.sendInstallSnapshotToNode(node, term, l.raft.id)

		return
	}

	lastLogEntryIndex, _ := l.raft.log.GetLastIndex()

	if next == 0 {
		next = lastLogEntryIndex + 1
	}

	var prevLogEntryIndex uint64

	if next > 0 {
		prevLogEntryIndex = next - 1
	}

	var prevLogEntryTerm uint64

	if prevLogEntryIndex > 0 {
		if prevLogEntryIndex == l.raft.snapshotIndex {
			prevLogEntryTerm = l.raft.snapshotTerm
		} else {
			raw, err := l.raft.log.Read(prevLogEntryIndex)

			if err == nil {
				if logEntry, err := DeserializeLogEntry(raw); err == nil {
					prevLogEntryTerm = logEntry.Term
				}
			}
		}
	}

	var logEntries []LogEntry

	if next <= lastLogEntryIndex {
		to := lastLogEntryIndex
		count := to - next + 1

		if count > maxBatch {
			to = next + maxBatch - 1
		}

		logEntries = make([]LogEntry, 0, to-next+1)

		for i := next; i <= to; i++ {
			raw, err := l.raft.log.Read(i)

			if err == nil {
				logEntry, err := DeserializeLogEntry(raw)
				if err == nil {
					logEntries = append(logEntries, *logEntry)
				} else {
					break
				}
			} else {
				break
			}
		}
	}

	go l.sendAppendEntriesToNode(node, term, l.raft.GetLeaderId(), prevLogEntryIndex, prevLogEntryTerm, logEntries, l.raft.store.commitIndex.Load())
}

func (l *LeaderRole) sendInstallSnapshotToNode(node string, term uint64, leaderId string) {
	snapshotPath := filepath.Join(l.raft.dataDir, "snapshot.data")

	snapshot, err := LoadSnapshot(snapshotPath)

	if err != nil {
		l.raft.mu.Lock()
		l.inflight[node] = false
		l.raft.mu.Unlock()

		return
	}

	t := l.raft.transport.InstallSnapshot(node, term, leaderId, snapshot.LastIncludedIndex, snapshot.LastIncludedTerm, snapshot.Data)

	l.raft.mu.Lock()
	defer l.raft.mu.Unlock()

	l.inflight[node] = false

	if t > l.raft.store.GetCurrentTerm() {
		l.raft.becomeFollower(t)

		return
	}

	if l.raft.role.GetType() != "leader" {
		return
	}

	if l.matchIndex[node] < snapshot.LastIncludedIndex {
		l.matchIndex[node] = snapshot.LastIncludedIndex
	}

	if l.nextIndex[node] < snapshot.LastIncludedIndex+1 {
		l.nextIndex[node] = snapshot.LastIncludedIndex + 1
	}

	l.tryToAdvanceCommitIndex()
}
