package internal

import (
	"context"
	"sort"
	"sync"
)

type LeaderRole struct {
	raft              *Raft
	heartbeatDeadline int
	heartbeatTicks    int
	matchIndex        sync.Map
	nextIndex         sync.Map
	pending           map[uint64][]chan any
}

func NewLeaderRole(raft *Raft) *LeaderRole {
	return &LeaderRole{
		raft:              raft,
		heartbeatDeadline: 15,
		heartbeatTicks:    0,
		pending:           make(map[uint64][]chan any),
	}
}

func (l *LeaderRole) GetType() string {
	return "leader"
}

func (l *LeaderRole) OnEnter(term uint64) {
	currentTerm := l.raft.store.GetCurrentTerm()

	if currentTerm != term {
		followerRole := l.raft.becomeFollower()

		followerRole.OnEnter(term)

		return
	}

	l.raft.store.SetLeaderId(l.raft.id)

	lastLogEntryIndex, _ := l.raft.log.GetLastIndex()

	for _, p := range l.raft.nodes {
		if p == l.raft.id {
			continue
		}

		l.matchIndex.Store(p, uint64(0))
		l.nextIndex.Store(p, lastLogEntryIndex+1)
	}

	l.matchIndex.Store(l.raft.id, lastLogEntryIndex+1)
	l.nextIndex.Store(l.raft.id, lastLogEntryIndex+1)

	logEntry := LogEntry{
		Data: nil,
		Term: term,
	}

	_, err := l.raft.log.SerializeAndWrite(&logEntry)

	if err != nil {
		followerRole := l.raft.becomeFollower()

		followerRole.OnEnter(term)

		return
	}

	l.raft.log.Commit()

	lastLogEntryIndex, err = l.raft.log.GetLastIndex()

	if err != nil {
		followerRole := l.raft.becomeFollower()

		followerRole.OnEnter(term)

		return
	}

	l.matchIndex.Store(l.raft.id, lastLogEntryIndex)
	l.nextIndex.Store(l.raft.id, lastLogEntryIndex+1)

	l.sendAppendEntriesToAllNodes()

	l.tryToAdvanceCommitIndex()
}

func (l *LeaderRole) OnExit() {
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

	followerRole := l.raft.becomeFollower()

	followerRole.OnEnter(term)

	return followerRole.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommit)
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

	followerRole := l.raft.becomeFollower()

	followerRole.OnEnter(term)

	return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (l *LeaderRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	currentTerm := l.raft.store.GetCurrentTerm()

	logEntry := LogEntry{
		Data: data,
		Term: currentTerm,
	}

	index, err := l.raft.log.SerializeAndWrite(&logEntry)

	if err != nil {
		return nil, err
	}

	ch := make(chan any, 1)

	l.pending[index] = append(l.pending[index], ch)

	// l.sendAppendEntriesToAllNodesLocked()

	value, _ := l.matchIndex.Load(l.raft.id)

	if value.(uint64) < index {
		l.matchIndex.Store(l.raft.id, index)
	}

	value, _ = l.nextIndex.Load(l.raft.id)

	if value.(uint64) < index+1 {
		l.nextIndex.Store(l.raft.id, index+1)
	}

	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
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

		return nil, ctx.Err()
	}
}

func (l *LeaderRole) applyToFiniteStateMachine() {
	// l.raft.applyMu.Lock()
	// defer l.raft.applyMu.Unlock()

	commitIndex := l.raft.store.commitIndex.Load()
	lastApplied := l.raft.store.lastApplied

	if commitIndex <= lastApplied {
		return
	}

	for idx := lastApplied + 1; idx <= commitIndex; idx++ {
		entry, err := l.raft.log.ReadAndDeserialize(idx)

		if err != nil {
			break
		}

		var result any

		if len(entry.Data) > 0 {
			result = l.raft.fsm.Apply(entry.Data)
		}

		waiters := l.pending[idx]

		if len(waiters) > 0 {
			delete(l.pending, idx)
		}

		for _, ch := range waiters {
			if result == nil && len(entry.Data) > 0 {
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
		value, _ := l.matchIndex.Load(n)

		matches = append(matches, value.(uint64))
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

	logEntry, err := l.raft.log.ReadAndDeserialize(candidate)

	if err != nil {
		return
	}

	if logEntry.Term != currentTerm {
		return
	}

	if candidate > l.raft.store.commitIndex.Load() {
		l.raft.setCommitIndex(candidate)

		l.applyToFiniteStateMachine()
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

		next, _ := l.nextIndex.Load(node)

		if next == 0 {
			next = lastLogEntryIndex + 1
		}

		var prevLogEntryIndex uint64

		if next.(uint64) > 0 {
			prevLogEntryIndex = next.(uint64) - 1
		}

		var prevLogEntryTerm uint64

		if prevLogEntryIndex > 0 {
			logEntry, err := l.raft.log.ReadAndDeserialize(prevLogEntryIndex)

			if err == nil {
				prevLogEntryTerm = logEntry.Term
			}
		}

		var logEntries []LogEntry

		if next.(uint64) <= lastLogEntryIndex {
			const maxBatch = 2 * 1024
			to := lastLogEntryIndex
			count := to - next.(uint64) + 1

			if count > maxBatch {
				to = next.(uint64) + maxBatch - 1
			}

			logEntries = make([]LogEntry, 0, to-next.(uint64)+1)

			for i := next.(uint64); i <= to; i++ {
				logEntry, err := l.raft.log.ReadAndDeserialize(i)

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

	if t > l.raft.store.GetCurrentTerm() {
		followerRole := l.raft.becomeFollower()

		followerRole.OnEnter(t)

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

		value, _ := l.matchIndex.Load(node)

		if value.(uint64) < lastSent {
			l.matchIndex.Store(node, lastSent)
		}

		next := lastSent + 1

		value, _ = l.nextIndex.Load(node)

		if value.(uint64) < next {
			l.nextIndex.Store(node, next)
		}

		l.tryToAdvanceCommitIndex()
	} else {
		value, _ := l.nextIndex.Load(node)

		if value.(uint64) > 1 {
			if conflictTerm != 0 {
				lastOfTerm := GetLastLogEntryIndexOfTerm(l.raft.log, conflictTerm)

				if lastOfTerm > 0 {
					l.nextIndex.Store(node, lastOfTerm+1)
				} else {
					l.nextIndex.Store(node, conflictIndex)
				}
			} else {
				l.nextIndex.Store(node, conflictIndex)
			}

			value, _ = l.nextIndex.Load(node)

			if value.(uint64) < 1 {
				l.nextIndex.Store(node, uint64(1))
			}
		}
	}
}
