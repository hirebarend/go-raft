package counter

import (
	"hash/maphash"
	"sync"
)

type Counter struct {
	minute  int16
	mu      sync.Mutex
	present map[uint32]bool
	seed    maphash.Seed
	slots   []map[uint32]bool
	value   int64
}

const numberOfSlots = 1440

func NewCounter() *Counter {
	slots := make([]map[uint32]bool, numberOfSlots)

	for i := range slots {
		slots[i] = make(map[uint32]bool)
	}

	return &Counter{
		minute:  -1,
		present: make(map[uint32]bool),
		seed:    maphash.MakeSeed(),
		slots:   slots,
		value:   0,
	}
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

	hash := uint32(maphash.String(c.seed, id))

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
