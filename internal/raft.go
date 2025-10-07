package internal

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	golog "github.com/hirebarend/go-log"
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
	) (uint64, bool, uint64, uint64)

	HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool)

	HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool)

	HandlePropose(ctx context.Context, data []byte) (any, error)

	Tick()
}

type Raft struct {
	applierMu   *sync.Mutex
	applierCond *sync.Cond
	fsm         *FSM
	id          string
	log         *golog.Log[LogEntry]
	mu          *sync.Mutex
	nodes       []string
	rng         *rand.Rand
	role        RaftRole
	roleMu      *sync.Mutex
	store       *Store
	transport   *Transport
}

func NewRaft(id string, nodes []string, log *golog.Log[LogEntry], store *Store, transport *Transport, fsm *FSM) *Raft {
	seed := uint64(time.Now().UnixNano())

	applierMu := &sync.Mutex{}

	raft := Raft{
		applierCond: sync.NewCond(applierMu),
		applierMu:   applierMu,
		fsm:         fsm,
		id:          id,
		log:         log,
		mu:          &sync.Mutex{},
		nodes:       nodes,
		rng:         rand.New(rand.NewPCG(seed, seed>>1)),
		role:        nil,
		roleMu:      &sync.Mutex{},
		store:       store,
		transport:   transport,
	}

	raft.becomeFollower()

	raft.role.OnEnter(0)

	return &raft
}

func (r *Raft) Tick() {
	r.role.Tick()
}

func (r *Raft) GetLeaderId() string {
	return r.store.GetLeaderId()
}

func (r *Raft) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommit uint64,
) (uint64, bool, uint64, uint64) {
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

// func (r *Raft) StartApplier() {
// 	for {
// 		r.applierMu.Lock()

// 		for r.store.lastApplied >= r.store.commitIndex {
// 			r.applierCond.Wait()
// 		}

// 		start := r.store.lastApplied + 1
// 		end := r.store.commitIndex

// 		r.applierMu.Unlock()

// 		results := make(map[uint64]any, end-start+1)

// 		for idx := start; idx <= end; idx++ {
// 			logEntry, err := r.log.ReadAndDeserialize(idx)

// 			if err != nil {
// 				return
// 			}

// 			results[idx] = r.fsm.Apply(logEntry.Data)
// 		}

// 		type notify struct {
// 			chans  []chan any
// 			result any
// 		}

// 		var toNotify []notify

// 		r.applierMu.Lock()

// 		r.mu.Lock()

// 		r.store.lastApplied = end

// 		for idx := start; idx <= end; idx++ {
// 			if ws, ok := r.pending[idx]; ok {
// 				delete(r.pending, idx)
// 				toNotify = append(toNotify, notify{
// 					chans:  ws,
// 					result: results[idx],
// 				})
// 			}
// 		}

// 		r.mu.Unlock()

// 		r.applierMu.Unlock()

// 		for i := range toNotify {
// 			ws := toNotify[i].chans
// 			res := toNotify[i].result

// 			for _, ch := range ws {
// 				select {
// 				case ch <- res:
// 				default:
// 				}
// 			}
// 		}
// 	}
// }

func (r *Raft) becomeCandidate() *CandidateRole {
	role := NewCandidateRole(r)

	r.roleMu.Lock()

	r.role = role

	r.roleMu.Unlock()

	return role
}

func (r *Raft) becomeFollower() *FollowerRole {
	role := NewFollowerRole(r)

	r.roleMu.Lock()

	r.role = role

	r.roleMu.Unlock()

	return role
}

func (r *Raft) becomeLeader() *LeaderRole {
	role := NewLeaderRole(r)

	r.roleMu.Lock()

	r.role = role

	r.roleMu.Unlock()

	return role
}

func logIsUpToDate(myIndex, myTerm, otherIndex, otherTerm uint64) bool {
	return (otherTerm > myTerm) || (otherTerm == myTerm && otherIndex >= myIndex)
}

func (r *Raft) setCommitIndex(commitIndex uint64) {
	lastLogEntryIndex, err := r.log.GetLastIndex()

	if err != nil || lastLogEntryIndex == 0 {
		return
	}

	if commitIndex > lastLogEntryIndex {
		commitIndex = lastLogEntryIndex
	}

	for {
		current := r.store.commitIndex.Load()

		if commitIndex <= current {
			return
		}
		if r.store.commitIndex.CompareAndSwap(current, commitIndex) {
			return // success
		}
	}
}
