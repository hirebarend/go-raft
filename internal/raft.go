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
	cond               *sync.Cond
	electionDeadline   uint64
	electionTimeoutMin int
	electionTimeoutMax int
	fsm                *FSM
	heartbeatInterval  uint64
	heartbeatDeadline  uint64
	id                 string
	leaderId           string
	log                *golog.Log[LogEntry]
	mu                 *sync.Mutex
	nodes              []string
	pending            map[uint64][]chan any
	rng                *rand.Rand
	role               RaftRole
	store              *Store
	ticks              uint64
	transport          *Transport
}

func NewRaft(id string, nodes []string, log *golog.Log[LogEntry], store *Store, transport *Transport, fsm *FSM) *Raft {
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
		log:                log,
		mu:                 mu,
		nodes:              nodes,
		pending:            make(map[uint64][]chan any),
		rng:                rand.New(rand.NewPCG(seed, seed>>1)),
		role:               nil,
		store:              store,
		ticks:              0,
		transport:          transport,
	}

	raft.becomeFollower()

	raft.role.OnEnter(0)

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

func (r *Raft) StartApplier() {
	for {
		r.mu.Lock()

		for r.store.lastApplied >= r.store.commitIndex {
			r.cond.Wait()
		}

		r.store.lastApplied++

		idx := r.store.lastApplied

		r.mu.Unlock()

		logEntry, err := r.log.ReadAndDeserialize(idx)

		if err != nil {
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

func (r *Raft) becomeCandidate() *CandidateRole {
	fmt.Printf("[%v] %v -> candidate\n", r.id, r.role.GetType())

	role := NewCandidateRole(r)

	r.role = role

	return role
}

func (r *Raft) becomeFollower() *FollowerRole {
	if r.role != nil {
		fmt.Printf("[%v] %v -> follower\n", r.id, r.role.GetType())
	}

	role := NewFollowerRole(r)

	r.role = role

	return role
}

func (r *Raft) becomeLeader() *LeaderRole {
	fmt.Printf("[%v] %v -> leader\n", r.id, r.role.GetType())

	role := NewLeaderRole(r)

	r.role = role

	return role
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

		logEntryAtIdx, err := r.log.ReadAndDeserialize(idx)

		if err == nil {
			if logEntryAtIdx.Term != entry.Term {
				err := r.log.TruncateFrom(idx)

				if err == nil {
					for _, logEntry := range logEntries[i:] {
						_, err := r.log.SerializeAndWrite(&logEntry)

						if err != nil {
							break
						}
					}

					r.log.Commit() // TODO
				}

				return
			}

			continue
		}

		for _, logEntry := range logEntries[i:] {
			_, err := r.log.SerializeAndWrite(&logEntry)

			if err != nil {
				break
			}
		}

		r.log.Commit() // TODO

		return
	}
}

func (r *Raft) setCommitIndexLocked(commitIndex uint64) {
	if commitIndex <= r.store.commitIndex {
		return
	}

	lastLogEntryIndex, err := r.log.GetLastIndex()

	if err != nil {
		return
	}

	if lastLogEntryIndex == 0 {
		return
	}

	if lastLogEntryIndex > r.store.commitIndex {
		r.store.commitIndex = lastLogEntryIndex

		r.cond.Signal()

		return
	}

	r.store.commitIndex = commitIndex

	r.cond.Signal()
}
