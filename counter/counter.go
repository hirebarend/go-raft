package counter

import (
	"bytes"
	"encoding/gob"
	"hash/fnv"
	"sync"
)

type Counter struct {
	minute  int16
	mu      sync.Mutex
	present map[uint64]bool
	slots   []map[uint64]bool
	value   int64
}

const numberOfSlots = 1440

func NewCounter() *Counter {
	slots := make([]map[uint64]bool, numberOfSlots)

	for i := range slots {
		slots[i] = make(map[uint64]bool)
	}

	return &Counter{
		minute:  -1,
		present: make(map[uint64]bool),
		slots:   slots,
		value:   0,
	}
}

func (c *Counter) Retrieve() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.value
}

func (c *Counter) Increment(id string, n int64, m int16) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	v := c.value + n
	if v < 0 {
		return false
	}

	m = m % numberOfSlots

	if m != c.minute {
		c.clearMinuteRange(c.minute, m)

		c.minute = m
	}

	hash := hashString(id)

	if _, ok := c.present[hash]; ok {
		return true
	}

	c.present[hash] = true
	c.slots[m][hash] = true
	c.value = v

	return true
}

func (c *Counter) clearMinute(m int16) {
	slot := c.slots[m%numberOfSlots]

	for h := range slot {
		delete(c.present, h)
		delete(slot, h)
	}
}

func (c *Counter) clearMinuteRange(from int16, to int16) {
	if from == -1 {
		return
	}

	for i := (int16(from) + 1) % numberOfSlots; ; i = (i + 1) % numberOfSlots {
		c.clearMinute(i)

		if i == to {
			break
		}
	}
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))

	return h.Sum64()
}

type counterSnapshot struct {
	Minute  int16
	Present map[uint64]bool
	Slots   []map[uint64]bool
	Value   int64
}

func (c *Counter) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := counterSnapshot{
		Minute:  c.minute,
		Present: c.present,
		Slots:   c.slots,
		Value:   c.value,
	}

	var buf bytes.Buffer

	if err := gob.NewEncoder(&buf).Encode(state); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (c *Counter) Restore(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var state counterSnapshot

	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&state); err != nil {
		return err
	}

	c.minute = state.Minute
	c.present = state.Present
	c.slots = state.Slots
	c.value = state.Value

	return nil
}
