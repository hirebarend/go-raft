package internal

import "testing"

// Reply false if term < currentTerm (§5.1)
func TestHandleRequestVote1(t *testing.T) {
	candidateId := "127.0.0.1:8080"

	store := NewStore()
	raft := NewRaft(store)

	store.SetCurrentTerm(10)

	_, voteGranted := raft.HandleRequestVote(1, &candidateId, 0, 0)

	if voteGranted {
		t.Fatalf("expected %v, got %v", false, voteGranted)
	}
}

// Reply true if term > currentTerm (§5.1)
func TestHandleRequestVote2(t *testing.T) {
	candidateId := "127.0.0.1:8080"

	store := NewStore()
	raft := NewRaft(store)

	store.SetCurrentTerm(10)

	_, voteGranted := raft.HandleRequestVote(15, &candidateId, 0, 0)

	if !voteGranted {
		t.Fatalf("expected %v, got %v", true, voteGranted)
	}
}
