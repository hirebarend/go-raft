package internal

import "sync"

type Store struct {
	mu sync.RWMutex

	// persistent
	currentTerm uint64

	// persistent
	votedFor string

	// volatile
	commitIndex uint64

	// volatile
	lastApplied uint64
}

func NewStore() *Store {
	return &Store{
		currentTerm: 0,
		votedFor:    "",
		commitIndex: 0,
		lastApplied: 0,
	}
}

func (s *Store) GetCurrentTerm() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.currentTerm
}

func (s *Store) IncrementCurrentTerm() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentTerm = s.currentTerm + 1

	return s.currentTerm
}

func (s *Store) SetCurrentTerm(v uint64) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentTerm = v

	return s.currentTerm
}

func (s *Store) GetVotedFor() string {
	return s.votedFor
}

func (s *Store) SetVotedFor(votedFor string) {
	s.votedFor = votedFor
}
