package counter

import (
	"hash/fnv"
)

type Counter struct {
	minuteOfTheDay uint64
	present        map[uint32]bool
	slots          []map[uint32]bool
	value          int64
}

func NewCounter() *Counter {
	slots := make([]map[uint32]bool, 1440)

	for i := 0; i < len(slots); i++ {
		slots[i] = make(map[uint32]bool)
	}

	return &Counter{
		minuteOfTheDay: 0,
		present:        make(map[uint32]bool),
		slots:          slots,
		value:          0,
	}
}

func (c *Counter) ClearMinute(n uint64) {
	slot := c.slots[n]

	for x := range slot {
		if c.present[x] {
			delete(c.present, x)
		}

		delete(slot, x)
	}
}

func (c *Counter) Increment(id string, n int64, minuteOfTheDay uint64) bool {
	if c.value+n < 0 {
		return false
	}

	idN := hashStringToInt(id)

	if c.present[idN] {
		return true
	}

	c.present[idN] = true
	c.slots[minuteOfTheDay][idN] = true

	c.value = c.value + n

	if c.minuteOfTheDay != minuteOfTheDay {
		c.minuteOfTheDay = minuteOfTheDay
		c.ClearMinute(c.minuteOfTheDay)
	}

	return true
}

func hashStringToInt(s string) uint32 {
	h := fnv.New32a()

	h.Write([]byte(s))

	return h.Sum32()
}
