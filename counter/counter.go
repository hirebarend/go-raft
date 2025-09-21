package counter

import "hash/maphash"

type Counter struct {
	minute  int
	present map[uint32]int
	slots   []map[uint32]struct{}
	value   int64
}

const numberOfSlots = 1440

func NewCounter() *Counter {
	slots := make([]map[uint32]struct{}, numberOfSlots)

	for i := range slots {
		slots[i] = make(map[uint32]struct{})
	}

	return &Counter{
		minute:  -1,
		present: make(map[uint32]int),
		slots:   slots,
		value:   0,
	}
}

func (c *Counter) clearMinute(m int) {
	slot := c.slots[m]

	for h := range slot {
		if c.present[h] == m {
			delete(c.present, h)
		}
	}

	c.slots[m] = make(map[uint32]struct{})
}

func (c *Counter) Increment(id string, n int64, m int) bool {
	v := c.value + n

	if v < 0 {
		return false
	}

	if m != c.minute {
		c.clearMinute(m)

		c.minute = m
	}

	hash := stringToUInt32(id)

	if _, ok := c.present[hash]; ok {
		c.value = v

		return true
	}

	c.present[hash] = m
	c.slots[m][hash] = struct{}{}
	c.value = v

	return true
}

func stringToUInt32(s string) uint32 {
	var hash maphash.Hash

	hash.WriteString(s)

	return uint32(hash.Sum64())
}
