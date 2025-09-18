package internal

type Raft struct {
	store *Store
}

func NewRaft(store *Store) *Raft {
	return &Raft{
		store: store,
	}
}

func (r *Raft) HandleRequestVote(term int64, candidateId *string, lastLogIndex int64, lastLogTerm int64) (int64, bool) {
	currentTerm := r.store.GetCurrentTerm()

	// Reply false if term < currentTerm (§5.1)
	if term < currentTerm {
		return currentTerm, false
	}

	if term > currentTerm {
		currentTerm = r.store.SetCurrentTerm(term)

		r.store.votedFor = nil

		// r.role = Follower

		// r.resetElectionTimer()

		// r.leaderId = nil
	}

	logEntry := r.store.GetLastLogEntry()

	var myLastLogIndex, myLastLogTerm int64

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

		// r.resetElectionTimer()

		return currentTerm, true
	}

	return currentTerm, false
}
