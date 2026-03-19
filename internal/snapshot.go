package internal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Snapshot struct {
	LastIncludedIndex uint64
	LastIncludedTerm  uint64
	Data              []byte
}

func SaveSnapshot(name string, snapshot *Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("mkdir: %w", err)
	}

	tempName := name + ".tmp"

	f, err := os.OpenFile(tempName, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)

	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}

	if err := binary.Write(f, binary.LittleEndian, snapshot.LastIncludedIndex); err != nil {
		_ = f.Close()

		return fmt.Errorf("write lastIncludedIndex: %w", err)
	}

	if err := binary.Write(f, binary.LittleEndian, snapshot.LastIncludedTerm); err != nil {
		_ = f.Close()

		return fmt.Errorf("write lastIncludedTerm: %w", err)
	}

	if err := binary.Write(f, binary.LittleEndian, uint32(len(snapshot.Data))); err != nil {
		_ = f.Close()

		return fmt.Errorf("write data len: %w", err)
	}

	if _, err := f.Write(snapshot.Data); err != nil {
		_ = f.Close()

		return fmt.Errorf("write data: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()

		return fmt.Errorf("fsync: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}

	if err := os.Rename(tempName, name); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	directory, err := os.Open(filepath.Dir(name))

	if err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}

	return nil
}

func LoadSnapshot(name string) (*Snapshot, error) {
	f, err := os.Open(name)

	if err != nil {
		return nil, err
	}

	defer f.Close()

	var lastIncludedIndex uint64

	if err := binary.Read(f, binary.LittleEndian, &lastIncludedIndex); err != nil {
		return nil, fmt.Errorf("read lastIncludedIndex: %w", err)
	}

	var lastIncludedTerm uint64

	if err := binary.Read(f, binary.LittleEndian, &lastIncludedTerm); err != nil {
		return nil, fmt.Errorf("read lastIncludedTerm: %w", err)
	}

	var n uint32

	if err := binary.Read(f, binary.LittleEndian, &n); err != nil {
		return nil, fmt.Errorf("read data len: %w", err)
	}

	if n > 256<<20 {
		return nil, fmt.Errorf("snapshot data too large: %d", n)
	}

	data := make([]byte, n)

	if _, err := io.ReadFull(f, data); err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}

	return &Snapshot{
		LastIncludedIndex: lastIncludedIndex,
		LastIncludedTerm:  lastIncludedTerm,
		Data:              data,
	}, nil
}
