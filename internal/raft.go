package internal

import (
	"fmt"
	"math/rand/v2"
	"sync"
)

type Raft struct {
	electionDeadline   uint64
	electionTimeoutMin int
	electionTimeoutMax int
	heartbeatInterval  uint64
	heartbeatDeadline  uint64
	id                 string
	leaderId           string
	mu                 sync.Mutex
	nodes              []string
	rng                *rand.Rand
	role               string
	store              *Store
	ticks              uint64
	transport          *Transport
}

func NewRaft(id string, nodes []string, store *Store, transport *Transport) *Raft {
	raft := Raft{
		electionDeadline:   0,
		electionTimeoutMin: 10,
		electionTimeoutMax: 20,
		heartbeatInterval:  5,
		heartbeatDeadline:  0 + 5,
		id:                 id,
		leaderId:           "",
		nodes:              nodes,
		rng:                rand.New(rand.NewPCG(42, 54)),
		role:               "candidate",
		store:              store,
		ticks:              0,
		transport:          transport,
	}

	raft.resetElectionTimer()

	return &raft
}

// REVIEWED
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
		if r.ticks >= r.electionDeadline {
			r.startElection()
		}
		return
	}
}

func (r *Raft) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogIndex uint64,
	prevLogTerm uint64,
	entries []LogEntry,
	leaderCommit uint64,
) (uint64, bool) {
	currentTerm := r.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	if term > currentTerm {
		currentTerm = r.convertToFollower(term)
	}

	if leaderId != "" {
		r.leaderId = leaderId
	}

	if prevLogIndex > 0 {
		termAtPrevLogIndex, ok := r.store.log.GetTerm(prevLogIndex)

		if !ok || termAtPrevLogIndex != prevLogTerm {
			return currentTerm, false
		}
	}

	var lastIndex = prevLogIndex

	if entries == nil {
		goto COMMIT
	}

	for i, e := range entries {
		expected := int64(prevLogIndex) + 1 + int64(i)

		if e.Index != uint64(expected) {
			e.Index = uint64(expected)
		}

		if termAtIndex, ok := r.store.log.GetTerm(e.Index); ok {
			if termAtIndex != e.Term {
				r.store.log.Truncate(e.Index)

				r.store.log.Append(entries[i:])

				lastIndex = entries[len(entries)-1].Index

				goto COMMIT
			}

			lastIndex = e.Index

			continue
		} else {
			r.store.log.Append(entries[i:])

			lastIndex = entries[len(entries)-1].Index

			goto COMMIT
		}
	}

COMMIT:
	if lastIndex == 0 {
		lastIndex, _ = r.store.log.LastIndex()
	}

	if leaderCommit > r.store.commitIndex {
		newCommit := leaderCommit

		if newCommit > lastIndex {
			newCommit = lastIndex
		}

		if newCommit > r.store.commitIndex {
			r.store.commitIndex = newCommit
			// if r.applyCond != nil {
			// 	r.applyCond.Signal()
			// }
		}
	}

	r.resetElectionTimer()

	return currentTerm, true
}

// REVIEWED
func (r *Raft) HandleRequestVote(term uint64, candidateId string, lastLogIndex uint64, lastLogTerm uint64) (uint64, bool) {
	currentTerm := r.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	if term > currentTerm {
		currentTerm = r.convertToFollower(term)
	}

	myLastLogIndex, _ := r.store.log.LastIndex()
	myLastLogTerm, _ := r.store.log.LastTerm()

	if r.store.GetVotedFor() != "" && r.store.GetVotedFor() != candidateId {
		return currentTerm, false
	}

	if !((lastLogTerm > myLastLogTerm) ||
		(lastLogTerm == myLastLogTerm && lastLogIndex >= myLastLogIndex)) {
		return currentTerm, false
	}

	r.store.SetVotedFor(candidateId)

	r.resetElectionTimer()

	return currentTerm, true
}

// REVIEWED
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

	fmt.Println("I'm a leader now!")
	fmt.Println(r.id)

	term := r.store.GetCurrentTerm()

	if r.store.nextIndex == nil {
		r.store.nextIndex = make(map[string]uint64, len(r.nodes))
	}

	if r.store.matchIndex == nil {
		r.store.matchIndex = make(map[string]uint64, len(r.nodes))
	}

	lastLogIndex, _ := r.store.log.LastIndex()

	for _, p := range r.nodes {
		if p == r.id {
			continue
		}
		r.store.nextIndex[p] = lastLogIndex + 1
		r.store.matchIndex[p] = 0
	}

	r.store.matchIndex[r.id] = lastLogIndex
	r.store.nextIndex[r.id] = lastLogIndex + 1

	logEntry := LogEntry{
		Index: lastLogIndex + 1,
		Term:  term,
		// Data:  nil,
	}

	if err := r.store.log.Append([]LogEntry{logEntry}); err == nil {
		if lastLogEntry, ok := r.store.log.Last(); ok {
			r.store.matchIndex[r.id] = lastLogEntry.Index
			r.store.nextIndex[r.id] = lastLogEntry.Index + 1
		}
	}

	r.sendAppendEntriesToAllNodes()

	r.resetHeartbeatTimer()

	// // 6) Try to advance commitIndex in case the no-op immediately forms a majority
	// r.maybeAdvanceCommitLocked()
}

func (r *Raft) resetElectionTimer() {
	r.electionDeadline = r.ticks + uint64(r.electionTimeoutMin+r.rng.IntN(r.electionTimeoutMax-r.electionTimeoutMin+1))
}

func (r *Raft) resetHeartbeatTimer() {
	r.heartbeatDeadline = r.ticks + r.heartbeatInterval
}

// REVIEWED
func (r *Raft) startElection() {
	currentTerm := r.store.SetCurrentTerm(r.store.GetCurrentTerm() + 1)
	r.role = "candidate"

	numberOfVotes := 1
	r.store.SetVotedFor(r.id)

	r.resetElectionTimer()

	lastLogIndex, _ := r.store.log.LastIndex()
	lastLogTerm, _ := r.store.log.LastTerm()

	for _, node := range r.nodes {
		if node == r.id {
			continue
		}

		go func() {
			term, voteGranted := r.transport.RequestVote(node, currentTerm, r.id, lastLogIndex, lastLogTerm)

			r.mu.Lock()
			defer r.mu.Unlock()

			if term > currentTerm {
				r.convertToFollower(term)

				return
			}

			if r.role == "candidate" && term == currentTerm && voteGranted {
				numberOfVotes++

				if numberOfVotes > len(r.nodes)/2 {
					r.convertToLeader()
				}
			}
		}()
	}
}

func (r *Raft) sendAppendEntriesToAllNodes() {
	currentTerm := r.store.GetCurrentTerm()

	lastLogIndex, _ := r.store.log.LastIndex()
	leaderCommit := r.store.commitIndex

	for _, node := range r.nodes {
		if node == r.id {
			continue
		}

		next := r.store.nextIndex[node]

		if next == 0 {
			next = lastLogIndex + 1
		}

		var prevLogIndex uint64

		if next > 0 {
			prevLogIndex = next - 1
		}

		var prevLogTerm uint64

		if prevLogIndex > 0 {
			if t, ok := r.store.log.GetTerm(prevLogIndex); ok {
				prevLogTerm = t
			}
		}

		var entries []LogEntry

		if next <= lastLogIndex {
			const maxBatch = 128
			to := lastLogIndex
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

		go r.transport.AppendEntries(node, currentTerm, r.leaderId, prevLogIndex, prevLogTerm, entries, leaderCommit)
	}
}
