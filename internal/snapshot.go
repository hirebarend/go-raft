package internal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

type Snapshot struct {
	LastIncludedIndex uint64
	LastIncludedTerm  uint64
	Data              []byte
}

const maxSnapshotSize = 256 << 20 // 256 MiB

// Snapshot format version
const snapshotFormatV2 byte = 0x02

func SaveSnapshot(name string, snapshot *Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("mkdir: %w", err)
	}

	tempName := name + ".tmp"

	f, err := os.OpenFile(tempName, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)

	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}

	// Write format version byte
	if _, err := f.Write([]byte{snapshotFormatV2}); err != nil {
		_ = f.Close()
		return fmt.Errorf("write version: %w", err)
	}

	if err := binary.Write(f, binary.LittleEndian, snapshot.LastIncludedIndex); err != nil {
		_ = f.Close()

		return fmt.Errorf("write lastIncludedIndex: %w", err)
	}

	if err := binary.Write(f, binary.LittleEndian, snapshot.LastIncludedTerm); err != nil {
		_ = f.Close()

		return fmt.Errorf("write lastIncludedTerm: %w", err)
	}

	// V2: uint64 data length
	if err := binary.Write(f, binary.LittleEndian, uint64(len(snapshot.Data))); err != nil {
		_ = f.Close()

		return fmt.Errorf("write data len: %w", err)
	}

	if _, err := f.Write(snapshot.Data); err != nil {
		_ = f.Close()

		return fmt.Errorf("write data: %w", err)
	}

	// Write CRC32 of data
	checksum := crc32.ChecksumIEEE(snapshot.Data)
	if err := binary.Write(f, binary.LittleEndian, checksum); err != nil {
		_ = f.Close()

		return fmt.Errorf("write crc: %w", err)
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

	// Peek at first byte to determine format version
	var firstByte [1]byte
	if _, err := io.ReadFull(f, firstByte[:]); err != nil {
		return nil, fmt.Errorf("read first byte: %w", err)
	}

	if firstByte[0] == snapshotFormatV2 {
		return loadSnapshotV2(f)
	}

	// Legacy V1 format: first byte is part of LastIncludedIndex (uint64 LE)
	// Seek back and read as V1
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}
	return loadSnapshotV1(f)
}

func loadSnapshotV1(f *os.File) (*Snapshot, error) {
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

	if n > maxSnapshotSize {
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

func loadSnapshotV2(f *os.File) (*Snapshot, error) {
	var lastIncludedIndex uint64

	if err := binary.Read(f, binary.LittleEndian, &lastIncludedIndex); err != nil {
		return nil, fmt.Errorf("read lastIncludedIndex: %w", err)
	}

	var lastIncludedTerm uint64

	if err := binary.Read(f, binary.LittleEndian, &lastIncludedTerm); err != nil {
		return nil, fmt.Errorf("read lastIncludedTerm: %w", err)
	}

	var n uint64

	if err := binary.Read(f, binary.LittleEndian, &n); err != nil {
		return nil, fmt.Errorf("read data len: %w", err)
	}

	if n > maxSnapshotSize {
		return nil, fmt.Errorf("snapshot data too large: %d", n)
	}

	data := make([]byte, n)

	if _, err := io.ReadFull(f, data); err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}

	// Verify CRC32
	var expectedCRC uint32
	if err := binary.Read(f, binary.LittleEndian, &expectedCRC); err != nil {
		return nil, fmt.Errorf("read crc: %w", err)
	}

	actualCRC := crc32.ChecksumIEEE(data)
	if expectedCRC != actualCRC {
		return nil, fmt.Errorf("snapshot CRC mismatch: expected %08x, got %08x", expectedCRC, actualCRC)
	}

	return &Snapshot{
		LastIncludedIndex: lastIncludedIndex,
		LastIncludedTerm:  lastIncludedTerm,
		Data:              data,
	}, nil
}
