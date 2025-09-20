package internal

import "fmt"

type FSM struct {
}

func (fsm *FSM) Apply(data []byte) any {
	fmt.Printf("[%s] apply\n", 1)

	return "Hello World"
}
