package internal

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"time"
)

type Raft struct {
	cond               *sync.Cond
	condMutex          *sync.Mutex
	electionDeadline   uint64
	electionTimeoutMin int
	electionTimeoutMax int
	fsm                *FSM
	heartbeatInterval  uint64
	heartbeatDeadline  uint64
	id                 string
	leaderId           string
	mu                 sync.Mutex
	nodes              []string
	pending            map[uint64][]chan any
	rng                *rand.Rand
	role               string
	store              *Store
	ticks              uint64
	transport          *Transport
}

func NewRaft(id string, nodes []string, store *Store, transport *Transport, fsm *FSM) *Raft {
	seed := uint64(time.Now().UnixNano())

	condMutex := &sync.Mutex{}

	raft := Raft{
		cond:               sync.NewCond(condMutex),
		condMutex:          condMutex,
		electionDeadline:   0,
		electionTimeoutMin: 10,
		electionTimeoutMax: 20,
		fsm:                fsm,
		heartbeatInterval:  5,
		heartbeatDeadline:  0 + 5,
		id:                 id,
		leaderId:           "",
		mu:                 sync.Mutex{},
		nodes:              nodes,
		pending:            make(map[uint64][]chan any),
		rng:                rand.New(rand.NewPCG(seed, seed>>1)),
		role:               "candidate",
		store:              store,
		ticks:              0,
		transport:          transport,
	}

	raft.resetElectionTimer()

	return &raft
}

func (r *Raft) Tick() {
	r.ticks++

	switch r.role {
	case "leader":
		if r.ticks >= r.heartbeatDeadline {
			r.sendAppendEntriesToAllNodes()
			r.resetHeartbeatTimer()
		}

		return

	case "candidate", "follower":
		fmt.Printf("[%s] ticks: %v, electionDeadline: %v\n", r.id, r.ticks, r.electionDeadline)
		if r.ticks >= r.electionDeadline {
			r.startPreVote()
		}

		return
	}
}

func (r *Raft) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommit uint64,
) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	currentTerm := r.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	if term > currentTerm {
		currentTerm = r.convertToFollower(term)
	} else {
		if r.role != "follower" {
			r.role = "follower"
		}
	}

	if leaderId != "" {
		r.leaderId = leaderId
	}

	r.resetElectionTimer()

	if prevLogEntryIndex > 0 {
		if termAtPrevLogIndex, ok := r.store.log.GetTerm(prevLogEntryIndex); !ok || termAtPrevLogIndex != prevLogEntryTerm {
			r.resetElectionTimer()

			return currentTerm, false
		}
	}

	for i, entry := range logEntries {
		idx := prevLogEntryIndex + 1 + uint64(i)

		if logEntryTermAtIdx, ok := r.store.log.GetTerm(idx); ok {
			if logEntryTermAtIdx != entry.Term {
				r.store.log.Truncate(idx)

				r.store.log.Append(logEntries[i:])

				goto COMMIT
			}

			continue
		} else {
			r.store.log.Append(logEntries[i:])

			goto COMMIT
		}
	}

COMMIT:
	if leaderCommit > r.store.commitIndex {
		lastLogEntryIndex, _ := r.store.log.LastIndex()

		newCommit := leaderCommit

		if newCommit > lastLogEntryIndex {
			newCommit = lastLogEntryIndex
		}

		if newCommit > r.store.commitIndex {
			r.store.commitIndex = newCommit

			r.condMutex.Lock()
			r.cond.Signal()
			r.condMutex.Unlock()
		}
	}

	return currentTerm, true
}

func (r *Raft) HandlePreVote(term uint64, candidateId string, lastLogEntryIndex, lastLogEntryTerm uint64) (uint64, bool) {
	currentTerm := r.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	myLastLogEntryIndex, _ := r.store.log.LastIndex()

	myLastLogEntryTerm, _ := r.store.log.LastTerm()

	if !((lastLogEntryTerm > myLastLogEntryTerm) ||
		(lastLogEntryTerm == myLastLogEntryTerm && lastLogEntryIndex >= myLastLogEntryIndex)) {
		return currentTerm, false
	}

	return currentTerm, true
}

func (r *Raft) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	currentTerm := r.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	if term > currentTerm {
		currentTerm = r.convertToFollower(term)
	}

	if r.store.GetVotedFor() != "" && r.store.GetVotedFor() != candidateId {
		return currentTerm, false
	}

	myLastLogEntryIndex, _ := r.store.log.LastIndex()
	myLastLogEntryTerm, _ := r.store.log.LastTerm()

	if !((lastLogEntryTerm > myLastLogEntryTerm) ||
		(lastLogEntryTerm == myLastLogEntryTerm && lastLogEntryIndex >= myLastLogEntryIndex)) {
		return currentTerm, false
	}

	r.store.SetVotedFor(candidateId)

	r.resetElectionTimer()

	return currentTerm, true
}

func (r *Raft) Propose(ctx context.Context, data []byte) (any, error) {
	fmt.Printf("[%v] role: %v\n", r.id, r.role)
	if r.role != "leader" {
		return nil, fmt.Errorf("not leader")
	}

	currentTerm := r.store.GetCurrentTerm()

	lastLogEntryIndex, _ := r.store.log.LastIndex()

	logEntry := LogEntry{
		Data:  data,
		Index: lastLogEntryIndex + 1,
		Term:  currentTerm,
	}

	r.store.log.Append([]LogEntry{logEntry})

	ch := make(chan any, 1)

	r.pending[logEntry.Index] = append(r.pending[logEntry.Index], ch)

	if r.role == "leader" {
		r.sendAppendEntriesToAllNodes()
	}

	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		ws := r.pending[logEntry.Index]
		if len(ws) > 0 {
			for i := range ws {
				if ws[i] == ch {
					ws[i] = ws[len(ws)-1]
					ws = ws[:len(ws)-1]
					break
				}
			}

			if len(ws) > 0 {
				r.pending[logEntry.Index] = ws
			} else {
				delete(r.pending, logEntry.Index)
			}
		}

		return nil, ctx.Err()
	}
}

func (r *Raft) StartApplier() {
	for {
		r.condMutex.Lock()

		for r.store.lastApplied >= r.store.commitIndex {
			fmt.Println("Waiting")
			r.cond.Wait()
			fmt.Println("Done")
		}

		r.store.lastApplied++

		idx := r.store.lastApplied

		r.condMutex.Unlock()

		logEntry, ok := r.store.log.Get(idx)

		if !ok {
			return
		}

		result := r.fsm.Apply(logEntry.Data)

		r.condMutex.Lock()

		if ws, ok := r.pending[idx]; ok {
			delete(r.pending, idx)

			for _, ch := range ws {
				select {
				case ch <- result:
				default:
				}
			}
		}

		r.condMutex.Unlock()
	}
}

func (r *Raft) convertToFollower(term uint64) uint64 {
	currentTerm := r.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm
	}

	if term > currentTerm {
		currentTerm = r.store.SetCurrentTerm(term)
	}

	r.store.SetVotedFor("")

	r.role = "follower"

	r.resetElectionTimer()

	// r.leaderId = ""

	return currentTerm
}

func (r *Raft) convertToLeader() {
	r.role = "leader"
	r.leaderId = r.id

	term := r.store.GetCurrentTerm()

	if r.store.nextIndex == nil {
		r.store.nextIndex = make(map[string]uint64, len(r.nodes))
	}

	if r.store.matchIndex == nil {
		r.store.matchIndex = make(map[string]uint64, len(r.nodes))
	}

	lastLogEntryIndex, _ := r.store.log.LastIndex()

	for _, p := range r.nodes {
		if p == r.id {
			continue
		}

		r.store.nextIndex[p] = lastLogEntryIndex + 1
		r.store.matchIndex[p] = 0
	}

	r.store.matchIndex[r.id] = lastLogEntryIndex
	r.store.nextIndex[r.id] = lastLogEntryIndex + 1

	logEntry := LogEntry{
		Data:  nil,
		Index: lastLogEntryIndex + 1,
		Term:  term,
	}

	if err := r.store.log.Append([]LogEntry{logEntry}); err == nil {
		if lastLogEntry, ok := r.store.log.Last(); ok {
			r.store.matchIndex[r.id] = lastLogEntry.Index
			r.store.nextIndex[r.id] = lastLogEntry.Index + 1
		}
	}

	r.sendAppendEntriesToAllNodes()

	r.resetHeartbeatTimer()

	r.maybeAdvanceCommit()
}

func (r *Raft) maybeAdvanceCommit() {
	if r.role != "leader" {
		return
	}

	currentTerm := r.store.GetCurrentTerm()

	matches := make([]uint64, 0, len(r.nodes))

	for _, n := range r.nodes {
		matches = append(matches, r.store.matchIndex[n])
	}

	if len(matches) == 0 {
		return
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i] < matches[j] })

	candidate := matches[len(matches)-len(matches)/2+1]

	if candidate == 0 {
		return
	}

	if termAtCand, ok := r.store.log.GetTerm(candidate); !ok || termAtCand != currentTerm {
		return
	}

	if candidate > r.store.commitIndex {
		r.store.commitIndex = candidate

		r.condMutex.Lock()
		r.cond.Signal()
		r.condMutex.Unlock()
	}
}

func (r *Raft) resetElectionTimer() {
	r.electionDeadline = r.ticks + uint64(r.electionTimeoutMin+r.rng.IntN(r.electionTimeoutMax-r.electionTimeoutMin+1))
}

func (r *Raft) resetHeartbeatTimer() {
	r.heartbeatDeadline = r.ticks + r.heartbeatInterval
}

func (r *Raft) startElection() {
	currentTerm := r.store.SetCurrentTerm(r.store.GetCurrentTerm() + 1)
	r.role = "candidate"

	numberOfVotes := 1

	r.store.SetVotedFor(r.id)

	r.resetElectionTimer()

	lastLogEntryIndex, _ := r.store.log.LastIndex()
	lastLogEntryTerm, _ := r.store.log.LastTerm()

	for _, node := range r.nodes {
		if node == r.id {
			continue
		}

		go func(n string, t uint64, ci string, llei uint64, llet uint64) {
			term, voteGranted := r.transport.RequestVote(n, t, ci, llei, llet)

			if term > t {
				r.convertToFollower(term)

				return
			}

			if r.role == "candidate" && term == t && voteGranted {
				numberOfVotes++

				if numberOfVotes > len(r.nodes)/2 {
					r.convertToLeader()
				}
			}
		}(node, currentTerm, r.id, lastLogEntryIndex, lastLogEntryTerm)
	}
}

func (r *Raft) startPreVote() {
	if r.role == "leader" {
		return
	}

	currentTerm := r.store.GetCurrentTerm()

	lastLogEntryIndex, _ := r.store.log.LastIndex()

	lastLogEntryTerm, _ := r.store.log.LastTerm()

	r.resetElectionTimer()

	numberOfVotes := 1

	for _, node := range r.nodes {
		go func(n string, t uint64, ci string, llei uint64, llet uint64) {
			term, voteGranted := r.transport.PreVote(n, t, ci, llei, llet)

			if term > t {
				r.convertToFollower(term)

				return
			}

			if r.role == "leader" || t != r.store.GetCurrentTerm() {
				return
			}

			if voteGranted {
				numberOfVotes++

				if numberOfVotes >= len(r.nodes)/2+1 {
					r.startElection()
				}
			}
		}(node, currentTerm, r.id, lastLogEntryIndex, lastLogEntryTerm)
	}
}

func (r *Raft) sendAppendEntriesToAllNodes() {
	currentTerm := r.store.GetCurrentTerm()

	lastLogEntryIndex, _ := r.store.log.LastIndex()
	leaderCommit := r.store.commitIndex

	for _, node := range r.nodes {
		if node == r.id {
			continue
		}

		next := r.store.nextIndex[node]

		if next == 0 {
			next = lastLogEntryIndex + 1
		}

		var prevLogEntryIndex uint64

		if next > 0 {
			prevLogEntryIndex = next - 1
		}

		var prevLogTerm uint64

		if prevLogEntryIndex > 0 {
			if term, ok := r.store.log.GetTerm(prevLogEntryIndex); ok {
				prevLogTerm = term
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
				if e, ok := r.store.log.Get(i); ok {
					entries = append(entries, e)
				} else {
					break
				}
			}
		}

		go func(n string, t uint64, li string, plei uint64, plet uint64, le []LogEntry, lc uint64) {
			term, success := r.transport.AppendEntries(n, t, li, plei, plet, le, lc)

			if term > r.store.GetCurrentTerm() {
				r.convertToFollower(term)

				return
			}

			if r.role != "leader" || term != t {
				return
			}

			if success {
				lastSent := plei
				if len(le) > 0 {
					lastSent = le[len(le)-1].Index
				}

				if r.store.matchIndex[n] < lastSent {
					r.store.matchIndex[n] = lastSent
				}
				next := lastSent + 1
				if r.store.nextIndex[n] < next {
					r.store.nextIndex[n] = next
				}

				r.maybeAdvanceCommit()
			} else {
				if r.store.nextIndex[n] > 1 {
					r.store.nextIndex[n]--
				}
			}
		}(node, currentTerm, r.leaderId, prevLogEntryIndex, prevLogTerm, entries, leaderCommit)
	}
}
