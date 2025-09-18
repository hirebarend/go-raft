package internal

import "fmt"

type Transport struct {
}

func (t *Transport) RequestVote(node *string, term uint64, candidateId *string, lastLogIndex uint64, lastLogTerm uint64) (uint64, bool) {
	fmt.Println(*node)

	return term, true
}
