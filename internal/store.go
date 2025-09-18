package internal

type Store struct {
	// persistent
	currentTerm uint64

	// persistent
	votedFor *string

	// persistent
	log *Log

	// volatile
	commitIndex uint64

	// volatile
	lastApplied uint64

	// volatile (leader)
	nextIndex map[string]uint64

	// volatile (leader)
	matchIndex map[string]uint64
}

func NewStore(log *Log) *Store {
	return &Store{
		currentTerm: 0,
		votedFor:    nil,
		log:         log,
		commitIndex: 0,
		lastApplied: 0,
		nextIndex:   nil, // TODO:
		matchIndex:  nil, // TODO:
	}
}

func (s *Store) GetCurrentTerm() uint64 {
	return s.currentTerm
}

func (s *Store) SetCurrentTerm(v uint64) uint64 {
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

func (s *Store) GetLogEntry(index uint64) *LogEntry {
	return nil
}

func (s *Store) GetLogEntryTerm(index uint64) (uint64, bool) {
	return 0, true
}
