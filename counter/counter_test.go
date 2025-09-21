package counter

import (
	"strconv"
	"testing"
)

func BenchmarkIntMin(b *testing.B) {
	counter := NewCounter()

	const pool = 100_000
	keys := make([]string, pool)

	for i := 0; i < pool; i++ {
		keys[i] = "key_" + strconv.Itoa(i)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		counter.Increment(keys[i%pool], 1, uint64((i/200)%1440))
	}
}
