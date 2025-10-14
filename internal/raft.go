package internal

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	golog "github.com/hirebarend/go-log"
)

type RaftRole interface {
	GetType() string

	OnEnter(term uint64)

	OnExit()

	HandleAppendEntries(
		term uint64,
		leaderId string,
		prevLogEntryIndex uint64,
		prevLogEntryTerm uint64,
		logEntries []LogEntry,
		leaderCommitIndex uint64,
	) (uint64, bool, uint64, uint64)

	HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool)

	HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool)

	HandlePropose(ctx context.Context, data []byte) (any, error)

	Tick()
}

type Raft struct {
	enabled   bool
	fsm       *FSM
	id        string
	log       *golog.Log[LogEntry]
	mu        *sync.Mutex
	nodes     []string
	rng       *rand.Rand
	role      RaftRole
	store     *Store
	transport *Transport
}

func NewRaft(id string, nodes []string, log *golog.Log[LogEntry], store *Store, transport *Transport, fsm *FSM) *Raft {
	seed := uint64(time.Now().UnixNano())

	raft := Raft{
		enabled:   true,
		fsm:       fsm,
		id:        id,
		log:       log,
		mu:        &sync.Mutex{},
		nodes:     nodes,
		rng:       rand.New(rand.NewPCG(seed, seed>>1)),
		role:      nil,
		store:     store,
		transport: transport,
	}

	raft.becomeFollower(0)

	return &raft
}

func (r *Raft) Disable() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled {
		return
	}

	r.enabled = false

	if leaderRole, ok := r.role.(*LeaderRole); ok {
		leaderRole.OnExit()

		r.role = nil
	}

	r.store.SetLeaderId("")
}

func (r *Raft) Enable() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.enabled {
		return
	}

	currentTerm := r.store.GetCurrentTerm()

	r.becomeFollower(currentTerm)

	r.enabled = true
}

func (r *Raft) Tick() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled {
		return
	}

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
	leaderCommitIndex uint64,
) (uint64, bool, uint64, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled {
		return r.store.GetCurrentTerm(), false, 0, 0
	}

	return r.role.HandleAppendEntries(term, leaderId, prevLogEntryIndex, prevLogEntryTerm, logEntries, leaderCommitIndex)
}

func (r *Raft) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled {
		return r.store.GetCurrentTerm(), false
	}

	return r.role.HandlePreVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (r *Raft) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled {
		return r.store.GetCurrentTerm(), false
	}

	return r.role.HandleRequestVote(term, candidateId, lastLogEntryIndex, lastLogEntryTerm)
}

func (r *Raft) Propose(ctx context.Context, data []byte) (any, error) {
	if !r.enabled {
		return nil, errors.New("disabled (hard off)")
	}

	return r.role.HandlePropose(ctx, data)
}

func (r *Raft) becomeCandidate() *CandidateRole {
	fmt.Printf("[%v] candidate\n", r.id)

	if r.role != nil {
		r.role.OnExit()
	}

	role := NewCandidateRole(r)

	r.role = role

	r.role.OnEnter(0)

	return role
}

func (r *Raft) becomeFollower(term uint64) *FollowerRole {
	fmt.Printf("[%v][%v] follower\n", r.id, term)

	if r.role != nil {
		r.role.OnExit()
	}

	role := NewFollowerRole(r)

	r.role = role

	r.role.OnEnter(term)

	return role
}

func (r *Raft) becomeLeader(term uint64) *LeaderRole {
	fmt.Printf("[%v][%v] leader\n", r.id, term)

	if r.role != nil {
		r.role.OnExit()
	}

	role := NewLeaderRole(r)

	r.role = role

	r.role.OnEnter(term)

	return role
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
