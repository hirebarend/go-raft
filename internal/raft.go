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
	leaderId           *string
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
		electionTimeoutMin: 3,
		electionTimeoutMax: 5,
		heartbeatInterval:  2,
		heartbeatDeadline:  0 + 2,
		id:                 id,
		leaderId:           nil,
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

func (r *Raft) Tick() {
	r.ticks++

	if r.role != "leader" && r.ticks >= r.electionDeadline {
		r.startElection()

		fmt.Println("Hello")
	} else {
		fmt.Println("World")
	}
}

func (r *Raft) HandleAppendEntries(
	term uint64,
	leaderId *string,
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
		currentTerm = r.store.SetCurrentTerm(term)

		r.store.votedFor = nil

		r.role = "follower"

		r.resetElectionTimer()

		r.leaderId = nil
	}

	if leaderId != nil {
		r.leaderId = leaderId
	}

	if prevLogIndex > 0 {
		termAtPrevLogIndex, ok := r.store.GetLogEntryTerm(prevLogIndex)

		if !ok || termAtPrevLogIndex != prevLogTerm {
			return currentTerm, false
		}
	}

	var lastIndex = prevLogIndex
	for i, e := range entries {
		expected := int64(prevLogIndex) + 1 + int64(i)

		if e.Index != uint64(expected) {
			e.Index = uint64(expected)
		}

		if termAtIndex, ok := r.store.GetLogEntryTerm(e.Index); ok {
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
		if lastLogEntry := r.store.GetLastLogEntry(); lastLogEntry != nil {
			lastIndex = lastLogEntry.Index
		}
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

func (r *Raft) HandleRequestVote(term uint64, candidateId *string, lastLogIndex uint64, lastLogTerm uint64) (uint64, bool) {
	currentTerm := r.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	if term > currentTerm {
		currentTerm = r.store.SetCurrentTerm(term)

		r.store.votedFor = nil

		r.role = "follower"

		r.resetElectionTimer()

		r.leaderId = nil
	}

	logEntry := r.store.GetLastLogEntry()

	var myLastLogIndex, myLastLogTerm uint64

	if logEntry == nil {
		myLastLogIndex = 0
		myLastLogTerm = 0
	} else {
		myLastLogIndex = logEntry.Index
		myLastLogTerm = logEntry.Term
	}

	if r.store.votedFor != nil && *r.store.votedFor != *candidateId {
		return currentTerm, false
	}

	if (lastLogTerm > myLastLogTerm) ||
		(lastLogTerm == myLastLogTerm && lastLogIndex >= myLastLogIndex) {
		r.store.SetVotedFor(candidateId)

		r.resetElectionTimer()

		return currentTerm, true
	}

	return currentTerm, false
}

func (r *Raft) convertToLeader() {
	r.role = "leader"
	r.leaderId = &r.id

	fmt.Println("I'm a leader now!")
	fmt.Println(r.id)

	term := r.store.GetCurrentTerm()

	if r.store.nextIndex == nil {
		r.store.nextIndex = make(map[string]uint64, len(r.nodes))
	}

	if r.store.matchIndex == nil {
		r.store.matchIndex = make(map[string]uint64, len(r.nodes))
	}

	lastLogIndex := uint64(0)
	if lastLogEntry := r.store.GetLastLogEntry(); lastLogEntry != nil {
		lastLogIndex = lastLogEntry.Index
	}

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
		if lastLogEntry := r.store.GetLastLogEntry(); lastLogEntry != nil {
			r.store.matchIndex[r.id] = lastLogEntry.Index
			r.store.nextIndex[r.id] = lastLogEntry.Index + 1
			// lastLogIndex = lastLogEntry.Index
		}
	}

	r.resetHeartbeatTimer()

	// // 5) Send initial AppendEntries (heartbeats) to all followers
	// for _, p := range r.peers {
	//     if p == r.id { continue }
	//     // Typically done asynchronously; no lock inside if it reads state.
	//     go r.sendAppendEntries(p)
	// }

	// // 6) Try to advance commitIndex in case the no-op immediately forms a majority
	// r.maybeAdvanceCommitLocked()
}

func (r *Raft) resetElectionTimer() {
	r.electionDeadline = r.ticks + uint64(r.electionTimeoutMin+r.rng.IntN(r.electionTimeoutMax-r.electionTimeoutMin+1))
}

func (r *Raft) resetHeartbeatTimer() {
	r.heartbeatDeadline = r.ticks + r.heartbeatInterval
}

func (r *Raft) startElection() {
	//  Increment currentTerm
	currentTerm := r.store.SetCurrentTerm(r.store.GetCurrentTerm() + 1)
	r.role = "candidate"

	// Vote for self
	numberOfVotes := 1
	r.store.SetVotedFor(&r.id)

	// Reset election timer
	r.resetElectionTimer()

	lastLogIndex := uint64(0)
	lastLogTerm := uint64(0)

	if entry := r.store.GetLastLogEntry(); entry != nil {
		lastLogIndex = entry.Index
		lastLogTerm = entry.Term
	}

	for _, node := range r.nodes {
		if node == r.id {
			continue
		}

		go func(n string) {
			term, voteGranted := r.transport.RequestVote(&n, currentTerm, &r.id, lastLogIndex, lastLogTerm)

			r.mu.Lock()
			defer r.mu.Unlock()

			if term > r.store.GetCurrentTerm() {
				currentTerm = r.store.SetCurrentTerm(term)

				r.store.votedFor = nil

				r.role = "follower"

				r.resetElectionTimer()

				r.leaderId = nil

				return
			}

			if r.role == "candidate" && term == r.store.GetCurrentTerm() && voteGranted {
				numberOfVotes++

				if numberOfVotes > len(r.nodes)/2 {
					r.convertToLeader()
				}
			}
		}(node)
	}
}
