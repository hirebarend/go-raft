package internal

import (
	"time"

	"github.com/hirebarend/go-raft/counter"
)

// FSM is the interface that any replicated state machine must implement.
type FSM interface {
	// Apply applies a committed log entry to the state machine and returns a result.
	Apply(data []byte) any

	// Snapshot returns a serialized snapshot of the current FSM state.
	Snapshot() ([]byte, error)

	// Restore replaces the FSM state with the given snapshot data.
	Restore(data []byte) error
}

// CounterFSM is the default FSM implementation using a deduplicating counter.
type CounterFSM struct {
	counter *counter.Counter
}

func NewFSM() FSM {
	return &CounterFSM{
		counter: counter.NewCounter(),
	}
}

func (fsm *CounterFSM) Apply(data []byte) any {
	now := time.Now()
	hour := now.Hour()
	minute := now.Minute()

	fsm.counter.Increment(string(data), 1, int16((hour*60)+minute))

	return true
}

func (fsm *CounterFSM) Snapshot() ([]byte, error) {
	return fsm.counter.Snapshot()
}

func (fsm *CounterFSM) Restore(data []byte) error {
	return fsm.counter.Restore(data)
}
