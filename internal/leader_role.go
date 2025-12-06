package internal

import (
	"context"
	"sort"
)

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
		heartbeatDeadline: raft.heartbeatTimeout,
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

	logEntry := LogEntry{
		Data: nil,
		Term: term,
	}

	newLastLogEntryIndex, err := l.raft.log.SerializeWriteCommit(&logEntry)
	if err != nil {
		l.raft.becomeFollower(term)
		return
	}

	l.matchIndex[l.raft.id] = newLastLogEntryIndex
	l.nextIndex[l.raft.id] = newLastLogEntryIndex + 1

	l.sendAppendEntriesToAllNodes()

	l.tryToAdvanceCommitIndex()

	go l.applier()
}

func (l *LeaderRole) OnExit() {
	l.stopApplier()
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

	return l.raft.becomeFollower(term).HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommit)
}

func (l *LeaderRole) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	currentTerm := l.raft.store.GetCurrentTerm()

	return currentTerm, false
}

func (l *LeaderRole) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	currentTerm := l.raft.store.GetCurrentTerm()

	if term <= currentTerm {
		return currentTerm, false
	}

	return l.raft.becomeFollower(term).HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (l *LeaderRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	currentTerm := l.raft.store.GetCurrentTerm()

	logEntry := LogEntry{
		Data: data,
		Term: currentTerm,
	}

	index, err := l.raft.log.SerializeWriteCommit(&logEntry)

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
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		l.raft.mu.Lock()
		if waiters, ok := l.pending[index]; ok {
			for i, waiter := range waiters {
				if waiter == ch {
					l.pending[index] = append(waiters[:i], waiters[i+1:]...)
					break
				}
			}
			if len(l.pending[index]) == 0 {
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
	lastApplied := l.raft.store.lastApplied

	if commitIndex <= lastApplied {
		return
	}

	for idx := lastApplied + 1; idx <= commitIndex; idx++ {
		logEntry, err := l.raft.log.ReadDeserialize(idx)

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

		l.raft.store.lastApplied = idx
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

	logEntry, err := l.raft.log.ReadDeserialize(candidate)

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

		var prevLogEntryIndex uint64

		if next > 0 {
			prevLogEntryIndex = next - 1
		}

		var prevLogEntryTerm uint64

		if prevLogEntryIndex > 0 {
			logEntry, err := l.raft.log.ReadDeserialize(prevLogEntryIndex)

			if err == nil {
				prevLogEntryTerm = logEntry.Term
			}
		}

		var logEntries []LogEntry

		if next <= lastLogEntryIndex {
			const maxBatch = 2 * 1024
			to := lastLogEntryIndex
			count := to - next + 1

			if count > maxBatch {
				to = next + maxBatch - 1
			}

			logEntries = make([]LogEntry, 0, to-next+1)

			for i := next; i <= to; i++ {
				logEntry, err := l.raft.log.ReadDeserialize(i)

				if err == nil {
					logEntries = append(logEntries, *logEntry)
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
		}
	}
}
