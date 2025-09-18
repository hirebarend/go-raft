package internal

type Store struct {
	// persistent
	currentTerm int64

	// persistent
	votedFor *string

	// persistent
	log *Log

	// volatile
	commitIndex int64

	// volatile
	lastApplied int64

	// volatile (leader)
	nextIndex map[string]int

	// volatile (leader)
	matchIndex map[string]int
}

func NewStore() *Store {
	return &Store{
		currentTerm: 0,
		votedFor:    nil,
		log:         nil, // TODO:
		commitIndex: 0,
		lastApplied: 0,
		nextIndex:   nil, // TODO:
		matchIndex:  nil, // TODO:
	}
}

func (s *Store) GetCurrentTerm() int64 {
	return s.currentTerm
}

func (s *Store) SetCurrentTerm(v int64) int64 {
	s.currentTerm = v

	return s.currentTerm
}

func (s *Store) GetVotedFor() *string {
	return s.votedFor
}

func (s *Store) SetVotedFor(votedFor *string) {
	s.votedFor = votedFor
}

func (s *Store) GetLastLogEntry() *LogEntry {
	return nil
}
