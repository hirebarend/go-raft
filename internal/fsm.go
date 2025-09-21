package internal

type FSM struct {
}

func (fsm *FSM) Apply(data []byte) any {
	return "Hello World"
}
