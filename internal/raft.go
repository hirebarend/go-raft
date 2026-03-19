package internal

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
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

	HandleInstallSnapshot(term uint64, leaderId string, lastIncludedIndex uint64, lastIncludedTerm uint64, data []byte) uint64

	HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool)

	HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool)

	HandlePropose(ctx context.Context, data []byte) (any, error)

	Tick()
}

type Raft struct {
	dataDir       string
	enabled       bool
	fsm           *FSM
	id            string
	log           *golog.Log
	mu            *sync.Mutex
	nodes         []string
	rng           *rand.Rand
	role          RaftRole
	snapshotIndex uint64
	snapshotTerm  uint64
	store         *Store
	transport     *Transport
}

func NewRaft(id string, dataDir string, nodes []string, log *golog.Log, store *Store, transport *Transport, fsm *FSM) *Raft {
	seed := uint64(time.Now().UnixNano())

	raft := Raft{
		dataDir:       dataDir,
		enabled:       true,
		fsm:           fsm,
		id:            id,
		log:           log,
		mu:            &sync.Mutex{},
		nodes:         nodes,
		rng:           rand.New(rand.NewPCG(seed, seed>>1)),
		role:          nil,
		snapshotIndex: 0,
		snapshotTerm:  0,
		store:         store,
		transport:     transport,
	}

	snapshotPath := filepath.Join(dataDir, "snapshot.data")

	snapshot, err := LoadSnapshot(snapshotPath)

	if err == nil && snapshot.LastIncludedIndex > 0 {
		raft.snapshotIndex = snapshot.LastIncludedIndex
		raft.snapshotTerm = snapshot.LastIncludedTerm

		if err := fsm.Restore(snapshot.Data); err != nil {
			fmt.Printf("[%v] failed to restore snapshot: %v\n", id, err)
		} else {
			fmt.Printf("[%v] restored snapshot at index %v term %v\n", id, snapshot.LastIncludedIndex, snapshot.LastIncludedTerm)
		}

		if raft.store.commitIndex.Load() < snapshot.LastIncludedIndex {
			raft.store.commitIndex.Store(snapshot.LastIncludedIndex)
		}

		if raft.store.lastApplied.Load() < snapshot.LastIncludedIndex {
			raft.store.lastApplied.Store(snapshot.LastIncludedIndex)
		}
	}

	lastLogEntryIndex, _ := log.GetLastIndex()
	lastApplied := raft.store.lastApplied.Load()

	for idx := lastApplied + 1; idx <= lastLogEntryIndex; idx++ {
		data, err := log.Read(idx)

		if err != nil {
			break
		}

		logEntry, err := DeserializeLogEntry(data)

		if err != nil {
			break
		}

		if len(logEntry.Data) > 0 {
			fsm.Apply(logEntry.Data)
		}

		raft.store.lastApplied.Store(idx)
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

	r.role.OnExit()

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

func (r *Raft) HandleInstallSnapshot(term uint64, leaderId string, lastIncludedIndex uint64, lastIncludedTerm uint64, data []byte) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled {
		return r.store.GetCurrentTerm()
	}

	return r.role.HandleInstallSnapshot(term, leaderId, lastIncludedIndex, lastIncludedTerm, data)
}

const snapshotThreshold uint64 = 1000

func (r *Raft) TakeSnapshot() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	lastApplied := r.store.lastApplied.Load()

	if lastApplied <= r.snapshotIndex {
		return nil
	}

	if lastApplied-r.snapshotIndex < snapshotThreshold {
		return nil
	}

	var snapshotTerm uint64

	if lastApplied == r.snapshotIndex {
		snapshotTerm = r.snapshotTerm
	} else {
		raw, err := r.log.Read(lastApplied)

		if err != nil {
			return err
		}

		logEntry, err := DeserializeLogEntry(raw)

		if err != nil {
			return err
		}

		snapshotTerm = logEntry.Term
	}

	data, err := r.fsm.Snapshot()

	if err != nil {
		return err
	}

	snapshotPath := filepath.Join(r.dataDir, "snapshot.data")

	snapshot := &Snapshot{
		LastIncludedIndex: lastApplied,
		LastIncludedTerm:  snapshotTerm,
		Data:              data,
	}

	if err := SaveSnapshot(snapshotPath, snapshot); err != nil {
		return err
	}

	r.snapshotIndex = snapshot.LastIncludedIndex
	r.snapshotTerm = snapshot.LastIncludedTerm

	lastLogEntryIndex, _ := r.log.GetLastIndex()

	if lastLogEntryIndex > r.snapshotIndex {
		r.log.TruncateTo(r.snapshotIndex)
	}

	fmt.Printf("[%v] snapshot taken at index %v term %v\n", r.id, r.snapshotIndex, r.snapshotTerm)

	return nil
}

func (r *Raft) installSnapshot(lastIncludedIndex uint64, lastIncludedTerm uint64, data []byte) error {
	snapshotPath := filepath.Join(r.dataDir, "snapshot.data")

	snapshot := &Snapshot{
		LastIncludedIndex: lastIncludedIndex,
		LastIncludedTerm:  lastIncludedTerm,
		Data:              data,
	}

	if err := SaveSnapshot(snapshotPath, snapshot); err != nil {
		return err
	}

	if err := r.fsm.Restore(data); err != nil {
		return err
	}

	r.snapshotIndex = lastIncludedIndex
	r.snapshotTerm = lastIncludedTerm

	if r.store.commitIndex.Load() < lastIncludedIndex {
		r.store.commitIndex.Store(lastIncludedIndex)
	}

	if r.store.lastApplied.Load() < lastIncludedIndex {
		r.store.lastApplied.Store(lastIncludedIndex)
	}

	lastLogEntryIndex, _ := r.log.GetLastIndex()

	if lastLogEntryIndex > 0 && lastLogEntryIndex <= lastIncludedIndex {
		r.resetLog(lastIncludedIndex + 1)
	} else if lastLogEntryIndex > lastIncludedIndex {
		r.log.TruncateTo(lastIncludedIndex)
	}

	fmt.Printf("[%v] installed snapshot at index %v term %v\n", r.id, lastIncludedIndex, lastIncludedTerm)

	return nil
}

func (r *Raft) resetLog(startIndex uint64) {
	r.log.TruncateFrom(1)

	entries, err := os.ReadDir(r.dataDir)

	if err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".seg") {
				os.Remove(filepath.Join(r.dataDir, e.Name()))
			}
		}
	}

	segName := filepath.Join(r.dataDir, fmt.Sprintf("%020d.seg", startIndex))

	f, err := os.Create(segName)

	if err == nil {
		f.Close()
	}

	r.log.Load()
}

func (r *Raft) getLastLogEntryIndexAndTerm() (uint64, uint64) {
	index, term := GetLastLogEntryIndexAndTerm(r.log)

	if index > 0 {
		return index, term
	}

	return r.snapshotIndex, r.snapshotTerm
}

func (r *Raft) isLogEqualOrMoreRecent(index, term uint64) bool {
	myIndex, myTerm := r.getLastLogEntryIndexAndTerm()

	if term > myTerm {
		return true
	}

	if term == myTerm && index >= myIndex {
		return true
	}

	return false
}

func (r *Raft) Propose(ctx context.Context, data []byte) (any, error) {
	r.mu.Lock()

	if !r.enabled {
		r.mu.Unlock()

		return nil, errors.New("disabled (hard off)")
	}

	role := r.role
	r.mu.Unlock()

	return role.HandlePropose(ctx, data)
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
