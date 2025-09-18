package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Transport struct {
	client *http.Client
}

type RequestVoteRequest struct {
	Term         uint64 `json:"term"`
	CandidateID  string `json:"candidateId"`
	LastLogIndex uint64 `json:"lastLogIndex"`
	LastLogTerm  uint64 `json:"lastLogTerm"`
}

type RequestVoteResponse struct {
	Term        uint64 `json:"term"`
	VoteGranted bool   `json:"voteGranted"`
}

func (t *Transport) RequestVote(
	node *string,
	term uint64,
	candidateId *string,
	lastLogIndex uint64,
	lastLogTerm uint64,
) (uint64, bool) {
	request := RequestVoteRequest{
		Term:         term,
		CandidateID:  *candidateId,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  lastLogTerm,
	}

	data, err := json.Marshal(request)
	if err != nil {
		return term, false
	}

	if t.client == nil {
		t.client = &http.Client{Timeout: 2 * time.Second}
	}

	url := fmt.Sprintf("http://%s/request-vote", *node)

	resp, err := t.client.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return term, false
	}
	defer resp.Body.Close()

	var response RequestVoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return term, false
	}

	return response.Term, response.VoteGranted
}
