package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type Transport struct {
	client *http.Client
}

type AppendEntriesRequest struct {
	Term              uint64     `json:"term"`
	LeaderId          string     `json:"leader_id"`
	PrevLogEntryIndex uint64     `json:"prev_log_entry_index"`
	PrevLogEntryTerm  uint64     `json:"prev_log_entry_term"`
	LogEntries        []LogEntry `json:"log_entries"`
	LeaderCommitIndex uint64     `json:"leader_commit_index"`
}

type AppendEntriesResponse struct {
	Term          uint64 `json:"term"`
	Success       bool   `json:"success"`
	ConflictIndex uint64 `json:"conflict_index"`
	ConflictTerm  uint64 `json:"conflict_term"`
}

type PreVoteRequest struct {
	Term              uint64 `json:"term"`
	CandidateID       string `json:"candidate_id"`
	LastLogEntryIndex uint64 `json:"last_log_entry_index"`
	LastLogEntryTerm  uint64 `json:"last_log_entry_term"`
}

type PreVoteResponse struct {
	Term        uint64 `json:"term"`
	VoteGranted bool   `json:"vote_granted"`
}

type RequestVoteRequest struct {
	Term              uint64 `json:"term"`
	CandidateID       string `json:"candidate_id"`
	LastLogEntryIndex uint64 `json:"last_log_entry_index"`
	LastLogEntryTerm  uint64 `json:"last_log_entry_term"`
}

type RequestVoteResponse struct {
	Term        uint64 `json:"term"`
	VoteGranted bool   `json:"vote_granted"`
}

type InstallSnapshotRequest struct {
	Term              uint64 `json:"term"`
	LeaderId          string `json:"leader_id"`
	LastIncludedIndex uint64 `json:"last_included_index"`
	LastIncludedTerm  uint64 `json:"last_included_term"`
	Data              []byte `json:"data"`
}

type InstallSnapshotResponse struct {
	Term uint64 `json:"term"`
}

func NewTransport() *Transport {
	tr := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:       300 * time.Millisecond,
			KeepAlive:     15 * time.Second,
			FallbackDelay: -1,
		}).DialContext,
		DisableCompression:     true,
		ExpectContinueTimeout:  0,
		ForceAttemptHTTP2:      true,
		Proxy:                  nil,
		IdleConnTimeout:        30 * time.Second,
		MaxConnsPerHost:        128,
		MaxIdleConns:           512,
		MaxIdleConnsPerHost:    128,
		MaxResponseHeaderBytes: 4 << 10, // 4 KiB
		ResponseHeaderTimeout:  800 * time.Millisecond,
		TLSHandshakeTimeout:    500 * time.Millisecond,
	}

	return &Transport{
		client: &http.Client{
			Transport: tr,
			Timeout:   0,
		},
	}
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
		Term:              term,
		LeaderId:          leaderId,
		PrevLogEntryIndex: prevLogEntryIndex,
		PrevLogEntryTerm:  prevLogEntryTerm,
		LogEntries:        logEntries,
		LeaderCommitIndex: leaderCommitIndex,
	}

	data, err := json.Marshal(appendEntriesRequest)

	if err != nil {
		return term, false, 0, 0
	}

	url := fmt.Sprintf("http://%s/rpc/append-entries", node)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
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

func (t *Transport) Propose(
	node string,
	data []byte,
) (any, error) {
	url := fmt.Sprintf("http://%s/rpc/propose", node)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))

	if err != nil {
		return nil, err
	}

	request.Header.Set("Content-Type", "application/octet-stream")

	response, err := t.client.Do(request)

	if err != nil {
		return nil, err
	}

	defer func() {
		io.Copy(io.Discard, response.Body)

		response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("propose forwarding failed: status %d", response.StatusCode)
	}

	const maxBody = 1 << 20

	reader := io.LimitReader(response.Body, maxBody)

	var result struct {
		Result any `json:"result"`
	}

	decoder := json.NewDecoder(reader)

	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}

	if decoder.More() {
		return nil, nil
	}

	return result.Result, nil
}

func (t *Transport) PreVote(
	node string,
	term uint64,
	candidateId string,
	lastLogEntryIndex uint64,
	lastLogEntryTerm uint64,
) (uint64, bool) {
	preVoteRequest := PreVoteRequest{
		Term:              term,
		CandidateID:       candidateId,
		LastLogEntryIndex: lastLogEntryIndex,
		LastLogEntryTerm:  lastLogEntryTerm,
	}

	data, err := json.Marshal(preVoteRequest)

	if err != nil {
		return term, false
	}

	url := fmt.Sprintf("http://%s/rpc/pre-vote", node)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
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
		Term:              term,
		CandidateID:       candidateId,
		LastLogEntryIndex: lastLogEntryIndex,
		LastLogEntryTerm:  lastLogEntryTerm,
	}

	data, err := json.Marshal(requestVoteRequest)

	if err != nil {
		return term, false
	}

	url := fmt.Sprintf("http://%s/rpc/request-vote", node)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
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

func (t *Transport) InstallSnapshot(
	node string,
	term uint64,
	leaderId string,
	lastIncludedIndex uint64,
	lastIncludedTerm uint64,
	data []byte,
) uint64 {
	installSnapshotRequest := InstallSnapshotRequest{
		Term:              term,
		LeaderId:          leaderId,
		LastIncludedIndex: lastIncludedIndex,
		LastIncludedTerm:  lastIncludedTerm,
		Data:              data,
	}

	body, err := json.Marshal(installSnapshotRequest)

	if err != nil {
		return term
	}

	url := fmt.Sprintf("http://%s/rpc/install-snapshot", node)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))

	if err != nil {
		return term
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := t.client.Do(request)

	if err != nil {
		return term
	}

	defer func() {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return term
	}

	// No size limit for snapshot responses — they can be large
	var installSnapshotResponse InstallSnapshotResponse

	decoder := json.NewDecoder(response.Body)

	if err := decoder.Decode(&installSnapshotResponse); err != nil {
		return term
	}

	return installSnapshotResponse.Term
}
