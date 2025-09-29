package internal

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

type RaftRole interface {
	GetType() string

	OnEnter(term uint64)

	HandleAppendEntries(
		term uint64,
		leaderId string,
		prevLogEntryIndex uint64,
		prevLogEntryTerm uint64,
		logEntries []LogEntry,
		leaderCommit uint64,
	) (uint64, bool)

	HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool)

	HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool)

	HandlePropose(ctx context.Context, data []byte) (any, error)

	Tick()
}

type Raft struct {
	cond               *sync.Cond
	electionDeadline   uint64
	electionTimeoutMin int
	electionTimeoutMax int
	fsm                *FSM
	heartbeatInterval  uint64
	heartbeatDeadline  uint64
	id                 string
	leaderId           string
	mu                 *sync.Mutex
	nodes              []string
	pending            map[uint64][]chan any
	rng                *rand.Rand
	role               RaftRole
	store              *Store
	ticks              uint64
	transport          *Transport
}

func NewRaft(id string, nodes []string, store *Store, transport *Transport, fsm *FSM) *Raft {
	seed := uint64(time.Now().UnixNano())

	mu := &sync.Mutex{}

	raft := Raft{
		cond:               sync.NewCond(mu),
		electionDeadline:   0,
		electionTimeoutMin: 15 * 5,
		electionTimeoutMax: 15 * 6,
		fsm:                fsm,
		heartbeatInterval:  15,
		heartbeatDeadline:  0 + 15,
		id:                 id,
		leaderId:           "",
		mu:                 mu,
		nodes:              nodes,
		pending:            make(map[uint64][]chan any),
		rng:                rand.New(rand.NewPCG(seed, seed>>1)),
		role:               nil,
		store:              store,
		ticks:              0,
		transport:          transport,
	}

	raft.role = NewCandidateRole(&raft)

	raft.resetElectionTimer()

	return &raft
}

func (r *Raft) Tick() {
	r.ticks++

	r.role.Tick()
}

func (r *Raft) GetLeaderId() string {
	return r.leaderId
}

func (r *Raft) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommit uint64,
) (uint64, bool) {
	return r.role.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommit)
}

func (r *Raft) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	return r.role.HandlePreVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (r *Raft) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	return r.role.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (r *Raft) Propose(ctx context.Context, data []byte) (any, error) {
	return r.role.HandlePropose(ctx, data)
}

func (r *Raft) StartApplier() {
	for {
		r.mu.Lock()

		for r.store.lastApplied >= r.store.commitIndex {
			r.cond.Wait()
		}

		r.store.lastApplied++

		idx := r.store.lastApplied

		r.mu.Unlock()

		logEntry, ok := r.store.log.Get(idx)

		if !ok {
			return
		}

		result := r.fsm.Apply(logEntry.Data)

		r.mu.Lock()

		if ws, ok := r.pending[idx]; ok {
			delete(r.pending, idx)

			for _, ch := range ws {
				select {
				case ch <- result:
				default:
				}
			}
		}

		r.mu.Unlock()
	}
}

func (r *Raft) isLogEntryOkay(index uint64, term uint64) bool {
	if index == 0 {
		return true
	}

	prevLogEntry, ok := r.store.log.Get(index)

	if !ok || prevLogEntry == nil || prevLogEntry.Term != term {
		return false
	}

	return true
}

func (r *Raft) resetElectionTimer() {
	r.electionDeadline = r.ticks + uint64(r.electionTimeoutMin+r.rng.IntN(r.electionTimeoutMax-r.electionTimeoutMin+1))
}

func logIsUpToDate(myIndex, myTerm, otherIndex, otherTerm uint64) bool {
	return (otherTerm > myTerm) || (otherTerm == myTerm && otherIndex >= myIndex)
}

func (r *Raft) appendEntriesLocked(prevLogEntryIndex uint64, logEntries []LogEntry) {
	for i, entry := range logEntries {
		idx := prevLogEntryIndex + 1 + uint64(i)

		if logEntryTermAtIdx, ok := r.store.log.GetTerm(idx); ok {
			if logEntryTermAtIdx != entry.Term {
				r.store.log.Truncate(idx)
				r.store.log.Append(logEntries[i:])

				return
			}

			continue
		}

		r.store.log.Append(logEntries[i:])

		return
	}
}

func (r *Raft) setCommitIndexLocked(commitIndex uint64) {
	if commitIndex <= r.store.commitIndex {
		return
	}

	logEntry, ok := r.store.log.Last()

	if !ok {
		return
	}

	if logEntry.Index > r.store.commitIndex {
		r.store.commitIndex = logEntry.Index
		r.cond.Signal()

		return
	}

	r.store.commitIndex = commitIndex
	r.cond.Signal()
}
