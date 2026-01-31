package internal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

type Store struct {
	commitIndex atomic.Uint64

	currentTerm uint64

	lastApplied atomic.Uint64

	leaderId string

	mu sync.RWMutex

	name string

	votedFor string
}

func NewStore(name string) (*Store, error) {
	store := &Store{
		currentTerm: 0,
		leaderId:    "",
		name:        name,
		votedFor:    "",
	}

	err := store.read()

	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read persistent state: %w", err)
	}

	return store, nil
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

	s.write()

	return s.currentTerm
}

func (s *Store) SetCurrentTerm(v uint64) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentTerm = v

	s.write()

	return s.currentTerm
}

func (s *Store) GetLeaderId() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.leaderId
}

func (s *Store) SetLeaderId(leaderId string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.leaderId = leaderId

	return s.leaderId
}

func (s *Store) GetVotedFor() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.votedFor
}

func (s *Store) SetVotedFor(votedFor string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.votedFor = votedFor

	s.write()
}

func (s *Store) SetCurrentTermAndVotedFor(term uint64, votedFor string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentTerm = term
	s.votedFor = votedFor

	s.write()
}

func (s *Store) read() error {
	f, err := os.Open(s.name)

	if err != nil {
		return err
	}

	defer f.Close()

	var term uint64

	if err := binary.Read(f, binary.LittleEndian, &term); err != nil {
		return fmt.Errorf("read term: %w", err)
	}

	var n uint32
	if err := binary.Read(f, binary.LittleEndian, &n); err != nil {
		return fmt.Errorf("read votedFor len: %w", err)
	}

	if n > 10<<20 {
		return fmt.Errorf("votedFor too large: %d", n)
	}

	votedForBytes := make([]byte, n)
	if _, err := io.ReadFull(f, votedForBytes); err != nil {
		return fmt.Errorf("read votedFor: %w", err)
	}

	s.currentTerm = term
	s.votedFor = string(votedForBytes)

	return nil
}

func (s *Store) write() error {
	if err := os.MkdirAll(filepath.Dir(s.name), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("mkdir: %w", err)
	}

	tempName := s.name + ".tmp"

	f, err := os.OpenFile(tempName, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)

	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}

	if err := binary.Write(f, binary.LittleEndian, s.currentTerm); err != nil {
		_ = f.Close()

		return fmt.Errorf("write term: %w", err)
	}

	votedForBytes := []byte(s.votedFor)

	if err := binary.Write(f, binary.LittleEndian, uint32(len(votedForBytes))); err != nil {
		_ = f.Close()

		return fmt.Errorf("write votedFor len: %w", err)
	}

	if _, err := f.Write(votedForBytes); err != nil {
		_ = f.Close()

		return fmt.Errorf("write votedFor: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()

		return fmt.Errorf("fsync file: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}

	if err := os.Rename(tempName, s.name); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	directory, err := os.Open(filepath.Dir(s.name))

	if err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}

	return nil
}
