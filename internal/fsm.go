package internal

import (
	"time"

	"github.com/hirebarend/go-raft/counter"
)

type FSM struct {
	counter *counter.Counter
}

func NewFSM() *FSM {
	return &FSM{
		counter: counter.NewCounter(),
	}
}

func (fsm *FSM) Apply(data []byte) any {
	now := time.Now()
	hour := now.Hour()
	minute := now.Minute()

	fsm.counter.Increment(string(data), 1, int16((hour*60)+minute))

	return true
}

func (fsm *FSM) Snapshot() ([]byte, error) {
	return fsm.counter.Snapshot()
}

func (fsm *FSM) Restore(data []byte) error {
	return fsm.counter.Restore(data)
}
