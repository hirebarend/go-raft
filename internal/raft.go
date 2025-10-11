package internal

import (
	"context"
	"fmt"
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

func (r *Raft) becomeCandidate() *CandidateRole {
	fmt.Printf("[%v] candidate\n", r.id)

	role := NewCandidateRole(r)

	r.role = role

	r.role.OnEnter(0)

	return role
}

func (r *Raft) becomeFollower(term uint64) *FollowerRole {
	fmt.Printf("[%v][%v] follower\n", r.id, term)

	role := NewFollowerRole(r)

	r.role = role

	r.role.OnEnter(term)

	return role
}

func (r *Raft) becomeLeader(term uint64) *LeaderRole {
	fmt.Printf("[%v][%v] leader\n", r.id, term)

	role := NewLeaderRole(r)

	r.role = role

	r.role.OnEnter(term)

	return role
}

// TODO
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
