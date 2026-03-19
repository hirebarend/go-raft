package counter

import (
	"strconv"
	"sync/atomic"
	"testing"
)

func TestIncrement1(t *testing.T) {
	counter := NewCounter()

	counter.Increment("key_a", 1, 500)

	if counter.value != 1 {
		t.Fatalf("expected %v, got %v", 1, counter.value)
	}
}

func TestIncrement2(t *testing.T) {
	counter := NewCounter()

	counter.Increment("key_a", 1, 500)
	counter.Increment("key_a", 1, 600)

	if counter.value != 1 {
		t.Fatalf("expected %v, got %v", 1, counter.value)
	}
}

func TestIncrement3(t *testing.T) {
	counter := NewCounter()

	counter.Increment("key_a", 1, 500)
	counter.Increment("key_a", 1, 600)
	counter.Increment("key_b", 1, 100)
	counter.Increment("key_a", 1, 700)

	if counter.value != 3 {
		t.Fatalf("expected %v, got %v", 3, counter.value)
	}
}

func BenchmarkIncrement(b *testing.B) {
	counter := NewCounter()

	const pool = 200_000
	keys := make([]string, pool)

	for i := 0; i < pool; i++ {
		keys[i] = "key_" + strconv.Itoa(i)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		counter.Increment(keys[i%pool], 1, int16((i/4096)%1440))
	}
}

func BenchmarkIncrementParallel(b *testing.B) {
	counter := NewCounter()

	const pool = 200_000
	keys := make([]string, pool)
	for i := 0; i < pool; i++ {
		keys[i] = "key_" + strconv.Itoa(i)
	}

	b.ReportAllocs()

	var seq uint64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := atomic.AddUint64(&seq, 1) - 1
			key := keys[i%pool]
			minute := int16((i / 4096) % 1440)
			counter.Increment(key, 1, minute)
		}
	})
}

func TestSnapshotRestore(t *testing.T) {
	counter := NewCounter()

	counter.Increment("key_a", 1, 500)
	counter.Increment("key_b", 1, 500)
	counter.Increment("key_c", 1, 500)

	if counter.value != 3 {
		t.Fatalf("expected %v, got %v", 3, counter.value)
	}

	data, err := counter.Snapshot()

	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	restored := NewCounter()

	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	if restored.value != 3 {
		t.Fatalf("expected restored value %v, got %v", 3, restored.value)
	}

	// Deduplication should still work after restore
	restored.Increment("key_a", 1, 500)

	if restored.value != 3 {
		t.Fatalf("expected deduplicated value %v, got %v", 3, restored.value)
	}

	// New keys should still increment
	restored.Increment("key_d", 1, 500)

	if restored.value != 4 {
		t.Fatalf("expected value %v, got %v", 4, restored.value)
	}
}
