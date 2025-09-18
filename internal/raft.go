package internal

import (
	"fmt"
	"math/rand/v2"
	"sync"
)

type Raft struct {
	electionDeadline uint64

	electionTimeoutMin int

	electionTimeoutMax int

	id string

	leaderId *string

	mu sync.Mutex

	nodes []string

	rng *rand.Rand

	role string

	store *Store

	ticks uint64

	transport *Transport
}

func NewRaft(id string, nodes []string, store *Store, transport *Transport) *Raft {
	raft := Raft{
		electionDeadline:   0,
		electionTimeoutMin: 3,
		electionTimeoutMax: 5,
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

func (r *Raft) HandleRequestVote(term uint64, candidateId *string, lastLogIndex uint64, lastLogTerm uint64) (uint64, bool) {
	currentTerm := r.store.GetCurrentTerm()

	// Reply false if term < currentTerm (§5.1)
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

	// If votedFor is null or candidateId, and candidate’s log is at
	// least as up-to-date as receiver’s log, grant vote (§5.2, §5.4)
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

	// term := r.store.GetCurrentTerm()

	// 2) Initialize leader bookkeeping
	// if r.nextIndex == nil { r.nextIndex = make(map[string]int64, len(r.peers)) }
	// if r.matchIndex == nil { r.matchIndex = make(map[string]int64, len(r.peers)) }

	// lastIdx := int64(0)
	// if le := r.store.GetLastLogEntry(); le != nil {
	//     lastIdx = le.Index
	// }

	// for _, p := range r.peers {
	//     if p == r.id { continue }
	//     r.nextIndex[p]  = lastIdx + 1 // next entry to send
	//     r.matchIndex[p] = 0           // nothing known replicated on follower yet
	// }
	// // Leader’s own match/next reflect its log
	// r.matchIndex[r.id] = lastIdx
	// r.nextIndex[r.id]  = lastIdx + 1

	// // 3) Append a no-op entry in the new term (Raft §5.2)
	// //    This helps the leader commit an entry from its term, establishing authority.
	// noop := LogEntry{
	//     Index: lastIdx + 1,   // store.Append may fill this; adapt if so
	//     Term:  term,
	//     Data:  nil,           // or a known marker like []byte("noop")
	// }
	// if err := r.store.Append(noop); err == nil {
	//     // Update our own indices to include the no-op we just appended
	//     // (If your Append sets Index, re-read last entry instead)
	//     if le := r.store.GetLastLogEntry(); le != nil {
	//         r.matchIndex[r.id] = le.Index
	//         r.nextIndex[r.id]  = le.Index + 1
	//         lastIdx            = le.Index
	//     }
	// } else {
	//     // If append fails, it’s safer to step down; but at minimum log it.
	//     // r.logger.Printf("becomeLeader: failed to append noop: %v", err)
	// }

	// // 4) Reset heartbeat timer so we send heartbeats promptly under the tick model
	// r.resetHeartbeatTimerLocked()

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
