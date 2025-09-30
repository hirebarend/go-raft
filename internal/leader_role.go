package internal

import (
	"context"
	"sort"
)

type LeaderRole struct {
	raft *Raft
}

func NewLeaderRole(raft *Raft) *LeaderRole {
	return &LeaderRole{
		raft: raft,
	}
}

func (l *LeaderRole) GetType() string {
	return "leader"
}

func (l *LeaderRole) OnEnter(term uint64) {
	l.raft.mu.Lock()

	currentTerm := l.raft.store.GetCurrentTerm()

	if currentTerm != term {
		followerRole := NewFollowerRole(l.raft)

		l.raft.role = followerRole

		l.raft.mu.Unlock()

		followerRole.OnEnter(term)

		return
	}

	l.raft.leaderId = l.raft.id

	if l.raft.store.nextIndex == nil {
		l.raft.store.nextIndex = make(map[string]uint64, len(l.raft.nodes))
	}

	if l.raft.store.matchIndex == nil {
		l.raft.store.matchIndex = make(map[string]uint64, len(l.raft.nodes))
	}

	lastLogEntryIndex, _ := l.raft.log.GetLastIndex()

	for _, p := range l.raft.nodes {
		if p == l.raft.id {
			continue
		}

		l.raft.store.nextIndex[p] = lastLogEntryIndex + 1
		l.raft.store.matchIndex[p] = 0
	}

	l.raft.store.matchIndex[l.raft.id] = lastLogEntryIndex
	l.raft.store.nextIndex[l.raft.id] = lastLogEntryIndex + 1

	logEntry := LogEntry{
		Data:  nil,
		Index: 0,
		Term:  term,
	}

	_, err := l.raft.log.SerializeAndWrite(&logEntry)

	if err != nil {
		// TODO
	}

	lastLogEntryIndex, err = l.raft.log.GetLastIndex()

	if err != nil {
		// TODO
	}

	l.raft.store.matchIndex[l.raft.id] = lastLogEntryIndex
	l.raft.store.nextIndex[l.raft.id] = lastLogEntryIndex + 1

	l.sendAppendEntriesToAllNodesLocked()

	l.resetHeartbeatTimer()

	l.maybeAdvanceCommit()

	l.raft.mu.Unlock()
}

func (l *LeaderRole) OnExit() {
}

func (l *LeaderRole) Tick() {
	l.raft.mu.Lock()
	l.sendAppendEntriesToAllNodesLocked()
	l.raft.mu.Unlock()

	l.resetHeartbeatTimer()
}

func (l *LeaderRole) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommit uint64,
) (uint64, bool) {
	l.raft.mu.Lock()

	currentTerm := l.raft.store.GetCurrentTerm()

	if term < currentTerm {
		l.raft.mu.Unlock()

		return currentTerm, false
	}

	if term == currentTerm && leaderId == l.raft.id {
		l.raft.mu.Unlock()

		return currentTerm, false
	}

	followerRole := NewFollowerRole(l.raft)

	l.raft.role = followerRole

	l.raft.mu.Unlock()

	followerRole.OnEnter(term)

	return followerRole.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommit)
}

func (l *LeaderRole) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	l.raft.mu.Lock()
	defer l.raft.mu.Unlock()

	currentTerm := l.raft.store.GetCurrentTerm()

	return currentTerm, false
}

func (l *LeaderRole) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	l.raft.mu.Lock()

	currentTerm := l.raft.store.GetCurrentTerm()

	if term <= currentTerm {
		l.raft.mu.Unlock()

		return currentTerm, false
	}

	followerRole := NewFollowerRole(l.raft)

	l.raft.role = followerRole

	l.raft.mu.Unlock()

	followerRole.OnEnter(term)

	return followerRole.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (l *LeaderRole) HandlePropose(ctx context.Context, data []byte) (any, error) {
	l.raft.mu.Lock()

	currentTerm := l.raft.store.GetCurrentTerm()

	logEntry := LogEntry{
		Data:  data,
		Index: 0,
		Term:  currentTerm,
	}

	index, err := l.raft.log.SerializeAndWrite(&logEntry)

	if err != nil {
		return nil, err
	}

	ch := make(chan any, 1)

	l.raft.pending[index] = append(l.raft.pending[index], ch)

	l.sendAppendEntriesToAllNodesLocked()

	l.raft.mu.Unlock()

	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		l.raft.mu.Lock()

		ws := l.raft.pending[index]
		if len(ws) > 0 {
			for i := range ws {
				if ws[i] == ch {
					ws[i] = ws[len(ws)-1]
					ws = ws[:len(ws)-1]
					break
				}
			}
			if len(ws) > 0 {
				l.raft.pending[index] = ws
			} else {
				delete(l.raft.pending, index)
			}
		}

		l.raft.mu.Unlock()

		return nil, ctx.Err()
	}
}

func (l *LeaderRole) maybeAdvanceCommit() {
	currentTerm := l.raft.store.GetCurrentTerm()

	matches := make([]uint64, 0, len(l.raft.nodes))

	for _, n := range l.raft.nodes {
		matches = append(matches, l.raft.store.matchIndex[n])
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

	if candidate > l.raft.store.commitIndex {
		l.raft.store.commitIndex = candidate

		l.raft.cond.Signal()
	}
}

func (l *LeaderRole) resetHeartbeatTimer() {
	l.raft.heartbeatDeadline = l.raft.ticks + l.raft.heartbeatInterval
}

func (l *LeaderRole) sendAppendEntriesToAllNodesLocked() {
	currentTerm := l.raft.store.GetCurrentTerm()

	lastLogEntryIndex, _ := l.raft.log.GetLastIndex()

	for _, node := range l.raft.nodes {
		if node == l.raft.id {
			continue
		}

		next := l.raft.store.nextIndex[node]

		if next == 0 {
			next = lastLogEntryIndex + 1
		}

		var prevLogEntryIndex uint64

		if next > 0 {
			prevLogEntryIndex = next - 1
		}

		var prevLogEntryTerm uint64

		if prevLogEntryIndex > 0 {
			logEntry, err := l.raft.log.ReadAndDeserialize(prevLogEntryIndex)

			if err == nil {
				prevLogEntryTerm = logEntry.Term
			}
		}

		var entries []LogEntry

		if next <= lastLogEntryIndex {
			const maxBatch = 128
			to := lastLogEntryIndex
			count := to - next + 1

			if count > maxBatch {
				to = next + maxBatch - 1
			}

			entries = make([]LogEntry, 0, to-next+1)

			for i := next; i <= to; i++ {
				logEntry, err := l.raft.log.ReadAndDeserialize(i)

				if err == nil {
					entries = append(entries, *logEntry)
				} else {
					break
				}
			}
		}

		go func(n string, t uint64, li string, plei uint64, plet uint64, le []LogEntry, lc uint64) {
			term, success := l.raft.transport.AppendEntries(n, t, li, plei, plet, le, lc)

			l.raft.mu.Lock()

			if term > l.raft.store.GetCurrentTerm() {
				followerRole := NewFollowerRole(l.raft)

				l.raft.role = followerRole

				l.raft.mu.Unlock()

				followerRole.OnEnter(term)

				return
			}

			if l.raft.role.GetType() != "leader" || term != t {
				l.raft.mu.Unlock()

				return
			}

			if success {
				lastSent := plei

				if len(le) > 0 {
					lastSent = le[len(le)-1].Index
				}

				if l.raft.store.matchIndex[n] < lastSent {
					l.raft.store.matchIndex[n] = lastSent
				}

				next := lastSent + 1

				if l.raft.store.nextIndex[n] < next {
					l.raft.store.nextIndex[n] = next
				}

				l.maybeAdvanceCommit()

				l.raft.mu.Unlock()
			} else {
				if l.raft.store.nextIndex[n] > 1 {
					l.raft.store.nextIndex[n]--
				}

				l.raft.mu.Unlock()
			}
		}(node, currentTerm, l.raft.leaderId, prevLogEntryIndex, prevLogEntryTerm, entries, l.raft.store.commitIndex)
	}
}
