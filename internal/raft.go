package internal

import (
	"fmt"
	"math/rand/v2"
	"time"
)

type Raft struct {
	electionDeadline   uint64
	electionTimeoutMin int
	electionTimeoutMax int
	heartbeatInterval  uint64
	heartbeatDeadline  uint64
	id                 string
	leaderId           string
	// mu                 sync.Mutex
	nodes     []string
	rng       *rand.Rand
	role      string
	store     *Store
	ticks     uint64
	transport *Transport
}

func NewRaft(id string, nodes []string, store *Store, transport *Transport) *Raft {
	seed := uint64(time.Now().UnixNano())

	raft := Raft{
		electionDeadline:   0,
		electionTimeoutMin: 10,
		electionTimeoutMax: 20,
		heartbeatInterval:  5,
		heartbeatDeadline:  0 + 5,
		id:                 id,
		leaderId:           "",
		nodes:              nodes,
		rng:                rand.New(rand.NewPCG(seed, seed>>1)),
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
		fmt.Printf("ticks: %v, electionDeadline: %v\n", r.ticks, r.electionDeadline)
		if r.ticks >= r.electionDeadline {
			r.startPreVote()
		}

		return
	}
}

// REVIEWED
func (r *Raft) HandleAppendEntries(
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommit uint64,
) (uint64, bool) {
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

	if prevLogEntryIndex > 0 {
		if termAtPrevLogIndex, ok := r.store.log.GetTerm(prevLogEntryIndex); !ok || termAtPrevLogIndex != prevLogEntryTerm {
			r.resetElectionTimer()

			return currentTerm, false
		}
	}

	lastLogEntryIndex := prevLogEntryIndex

	for i, entry := range logEntries {
		idx := prevLogEntryIndex + 1 + uint64(i)

		if termAtIdx, ok := r.store.log.GetTerm(idx); ok { // TODO
			if termAtIdx != entry.Term {
				r.store.log.Truncate(idx)
				r.store.log.Append(logEntries[i:])
				lastLogEntryIndex = logEntries[len(logEntries)-1].Index
				goto COMMIT
			}

			lastLogEntryIndex = idx
			continue
		} else {
			r.store.log.Append(logEntries[i:])
			lastLogEntryIndex = logEntries[len(logEntries)-1].Index
			goto COMMIT
		}
	}

COMMIT:
	if lastLogEntryIndex == 0 {
		if logEntryIndex, ok := r.store.log.LastIndex(); ok {
			lastLogEntryIndex = logEntryIndex
		} else {
			r.resetElectionTimer()
			return currentTerm, false
		}
	}

	if leaderCommit > r.store.commitIndex {
		newCommit := leaderCommit
		if newCommit > lastLogEntryIndex {
			newCommit = lastLogEntryIndex
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

// REVIEWED
func (r *Raft) HandleRequestVote(term uint64, candidateId string, lastLogEntryIndex uint64, lastLogEntryTerm uint64) (uint64, bool) {
	currentTerm := r.store.GetCurrentTerm()

	if term < currentTerm {
		return currentTerm, false
	}

	if term > currentTerm {
		currentTerm = r.convertToFollower(term)
	}

	myLastLogEntryIndex, _ := r.store.log.LastIndex()
	myLastLogEntryTerm, _ := r.store.log.LastTerm()

	if r.store.GetVotedFor() != "" && r.store.GetVotedFor() != candidateId {
		return currentTerm, false
	}

	if !((lastLogEntryTerm > myLastLogEntryTerm) ||
		(lastLogEntryTerm == myLastLogEntryTerm && lastLogEntryIndex >= myLastLogEntryIndex)) {
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
		Index: lastLogEntryIndex + 1,
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

	lastLogEntryIndex, _ := r.store.log.LastIndex()
	lastLogEntryTerm, _ := r.store.log.LastTerm()

	for _, node := range r.nodes {
		if node == r.id {
			continue
		}

		go func() {
			term, voteGranted := r.transport.RequestVote(node, currentTerm, r.id, lastLogEntryIndex, lastLogEntryTerm)

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
		go func() {
			term, voteGranted := r.transport.PreVote(node, currentTerm, r.id, lastLogEntryIndex, lastLogEntryTerm)

			if term > r.store.GetCurrentTerm() {
				r.convertToFollower(term)

				return
			}

			if r.role == "leader" || currentTerm != r.store.GetCurrentTerm() {
				return
			}

			if voteGranted {
				numberOfVotes++

				if numberOfVotes >= len(r.nodes)/2+1 {
					r.startElection()
				}
			}
		}()
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

		go r.transport.AppendEntries(node, currentTerm, r.leaderId, prevLogEntryIndex, prevLogTerm, entries, leaderCommit)
	}
}
