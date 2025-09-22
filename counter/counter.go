package counter

import "hash/maphash"

type Counter struct {
	minute  int16
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

func (c *Counter) clearMinute(m int16) {
	slot := c.slots[m]

	for h := range slot {
		if c.present[h] {
			delete(c.present, h)
		}
	}

	c.slots[m] = make(map[uint32]bool)
}

func (c *Counter) clearMinuteRange(from int16, to int16) {
	if from == -1 {
		return
	}

	for i := (from + 1) % numberOfSlots; ; i = (i + 1) % numberOfSlots {
		c.clearMinute(i)

		if i == to {
			break
		}
	}
}

func (c *Counter) Increment(id string, n int64, m int16) bool {
	v := c.value + n

	if v < 0 {
		return false
	}

	if m != c.minute {
		c.clearMinuteRange(c.minute, m%numberOfSlots)

		c.minute = m
	}

	hash := uint32(maphash.String(c.seed, id))

	if _, ok := c.present[hash]; ok {
		c.value = v

		return true
	}

	c.present[hash] = true
	c.slots[m][hash] = true
	c.value = v

	return true
}
