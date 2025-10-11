package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

type Transport struct {
	client *http.Client
	once   sync.Once
}

type AppendEntriesRequest struct {
	Term         uint64     `json:"term"`
	LeaderId     string     `json:"leaderId"`
	PrevLogIndex uint64     `json:"prevLogIndex"`
	PrevLogTerm  uint64     `json:"prevLogTerm"`
	Entries      []LogEntry `json:"entries"`
	LeaderCommit uint64     `json:"leaderCommit"`
}

type AppendEntriesResponse struct {
	Term          uint64 `json:"term"`
	Success       bool   `json:"success"`
	ConflictIndex uint64 `json:"conflict_index"`
	ConflictTerm  uint64 `json:"conflict_term"`
}

type PreVoteRequest struct {
	Term         uint64 `json:"term"`
	CandidateID  string `json:"candidateId"`
	LastLogIndex uint64 `json:"lastLogIndex"`
	LastLogTerm  uint64 `json:"lastLogTerm"`
}

type PreVoteResponse struct {
	Term        uint64 `json:"term"`
	VoteGranted bool   `json:"voteGranted"`
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

func (t *Transport) ensureClient() {
	t.once.Do(func() {
		t.client = &http.Client{
			Timeout: 2 * time.Second,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   1 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   1 * time.Second,
				ResponseHeaderTimeout: 1500 * time.Millisecond,
				ExpectContinueTimeout: 1 * time.Second,
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   10,
			},
		}
	})
}
func (t *Transport) AppendEntries(
	node string,
	term uint64,
	leaderId string,
	prevLogEntryIndex uint64,
	prevLogEntryTerm uint64,
	logEntries []LogEntry,
	leaderCommitIndex uint64,
) (uint64, bool, uint64, uint64) {
	appendEntriesRequest := AppendEntriesRequest{
		Term:         term,
		LeaderId:     leaderId,
		PrevLogIndex: prevLogEntryIndex,
		PrevLogTerm:  prevLogEntryTerm,
		Entries:      logEntries,
		LeaderCommit: leaderCommitIndex,
	}

	data, err := json.Marshal(appendEntriesRequest)

	if err != nil {
		return term, false, 0, 0
	}

	t.ensureClient()

	url := fmt.Sprintf("http://%s/append-entries", node)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))

	if err != nil {
		return term, false, 0, 0
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := t.client.Do(request)

	if err != nil {
		return term, false, 0, 0
	}

	defer func() {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return term, false, 0, 0
	}

	const maxBody = 1 << 20

	reader := io.LimitReader(response.Body, maxBody)

	var appendEntriesResponse AppendEntriesResponse

	decoder := json.NewDecoder(reader)

	if err := decoder.Decode(&appendEntriesResponse); err != nil {
		return term, false, 0, 0
	}

	if decoder.More() {
		return term, false, 0, 0
	}

	return appendEntriesResponse.Term, appendEntriesResponse.Success, appendEntriesResponse.ConflictIndex, appendEntriesResponse.ConflictTerm
}

func (t *Transport) PreVote(
	node string,
	term uint64,
	candidateId string,
	lastLogEntryIndex uint64,
	lastLogEntryTerm uint64,
) (uint64, bool) {
	preVoteRequest := PreVoteRequest{
		Term:         term,
		CandidateID:  candidateId,
		LastLogIndex: lastLogEntryIndex,
		LastLogTerm:  lastLogEntryTerm,
	}

	data, err := json.Marshal(preVoteRequest)

	if err != nil {
		return term, false
	}

	t.ensureClient()

	url := fmt.Sprintf("http://%s/pre-vote", node)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))

	if err != nil {
		return term, false
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := t.client.Do(request)

	if err != nil {
		return term, false
	}

	defer func() {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return term, false
	}

	const maxBody = 1 << 20

	reader := io.LimitReader(response.Body, maxBody)

	var preVoteResponse PreVoteResponse

	decoder := json.NewDecoder(reader)

	if err := decoder.Decode(&preVoteResponse); err != nil {
		return term, false
	}

	if decoder.More() {
		return term, false
	}

	return preVoteResponse.Term, preVoteResponse.VoteGranted
}

func (t *Transport) RequestVote(
	node string,
	term uint64,
	candidateId string,
	lastLogEntryIndex uint64,
	lastLogEntryTerm uint64,
) (uint64, bool) {
	requestVoteRequest := RequestVoteRequest{
		Term:         term,
		CandidateID:  candidateId,
		LastLogIndex: lastLogEntryIndex,
		LastLogTerm:  lastLogEntryTerm,
	}

	data, err := json.Marshal(requestVoteRequest)

	if err != nil {
		return term, false
	}

	t.ensureClient()

	url := fmt.Sprintf("http://%s/request-vote", node)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))

	if err != nil {
		return term, false
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := t.client.Do(request)

	if err != nil {
		return term, false
	}

	defer func() {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return term, false
	}

	const maxBody = 1 << 20

	reader := io.LimitReader(response.Body, maxBody)

	var requestVoteResponse RequestVoteResponse

	decoder := json.NewDecoder(reader)

	if err := decoder.Decode(&requestVoteResponse); err != nil {
		return term, false
	}

	if decoder.More() {
		return term, false
	}

	return requestVoteResponse.Term, requestVoteResponse.VoteGranted
}
