package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadSnapshot(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "snapshot.data")

	snapshot := &Snapshot{
		LastIncludedIndex: 42,
		LastIncludedTerm:  5,
		Data:              []byte("hello snapshot"),
	}

	if err := SaveSnapshot(name, snapshot); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := LoadSnapshot(name)

	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if loaded.LastIncludedIndex != 42 {
		t.Fatalf("expected LastIncludedIndex 42, got %v", loaded.LastIncludedIndex)
	}

	if loaded.LastIncludedTerm != 5 {
		t.Fatalf("expected LastIncludedTerm 5, got %v", loaded.LastIncludedTerm)
	}

	if string(loaded.Data) != "hello snapshot" {
		t.Fatalf("expected data 'hello snapshot', got '%v'", string(loaded.Data))
	}
}

func TestLoadSnapshotNotFound(t *testing.T) {
	_, err := LoadSnapshot("/tmp/nonexistent-snapshot-file.data")

	if err == nil {
		t.Fatal("expected error for missing snapshot file")
	}
}

func TestSaveSnapshotAtomic(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "snapshot.data")

	snapshot1 := &Snapshot{
		LastIncludedIndex: 10,
		LastIncludedTerm:  1,
		Data:              []byte("first"),
	}

	if err := SaveSnapshot(name, snapshot1); err != nil {
		t.Fatalf("save 1 failed: %v", err)
	}

	snapshot2 := &Snapshot{
		LastIncludedIndex: 20,
		LastIncludedTerm:  2,
		Data:              []byte("second"),
	}

	if err := SaveSnapshot(name, snapshot2); err != nil {
		t.Fatalf("save 2 failed: %v", err)
	}

	loaded, err := LoadSnapshot(name)

	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if loaded.LastIncludedIndex != 20 {
		t.Fatalf("expected LastIncludedIndex 20, got %v", loaded.LastIncludedIndex)
	}

	if string(loaded.Data) != "second" {
		t.Fatalf("expected data 'second', got '%v'", string(loaded.Data))
	}

	// Temp file should be cleaned up
	if _, err := os.Stat(name + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("expected tmp file to be cleaned up")
	}
}
