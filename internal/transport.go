package internal

import (
	"bytes"
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
	Term    uint64 `json:"term"`
	Success bool   `json:"success"`
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
	prevLogIndex uint64,
	prevLogTerm uint64,
	entries []LogEntry,
	leaderCommit uint64,
) (uint64, bool) {
	request := AppendEntriesRequest{
		Term:         term,
		LeaderId:     leaderId,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      entries,
		LeaderCommit: leaderCommit,
	}

	data, err := json.Marshal(request)
	if err != nil {
		return term, false
	}

	t.ensureClient()

	url := fmt.Sprintf("http://%s/append-entries", node)

	resp, err := t.client.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return term, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return term, false
	}

	var response AppendEntriesResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return term, false
	}

	return response.Term, response.Success
}

func (t *Transport) PreVote(
	node string,
	term uint64,
	candidateId string,
	lastLogIndex uint64,
	lastLogTerm uint64,
) (uint64, bool) {
	request := PreVoteRequest{
		Term:         term,
		CandidateID:  candidateId,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  lastLogTerm,
	}

	data, err := json.Marshal(request)
	if err != nil {
		return term, false
	}

	t.ensureClient()

	url := fmt.Sprintf("http://%s/pre-vote", node)

	resp, err := t.client.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return term, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return term, false
	}

	var response PreVoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return term, false
	}

	return response.Term, response.VoteGranted
}

func (t *Transport) RequestVote(
	node string,
	term uint64,
	candidateId string,
	lastLogIndex uint64,
	lastLogTerm uint64,
) (uint64, bool) {
	request := RequestVoteRequest{
		Term:         term,
		CandidateID:  candidateId,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  lastLogTerm,
	}

	data, err := json.Marshal(request)
	if err != nil {
		return term, false
	}

	t.ensureClient()

	url := fmt.Sprintf("http://%s/request-vote", node)

	resp, err := t.client.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return term, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return term, false
	}

	var response RequestVoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return term, false
	}

	return response.Term, response.VoteGranted
}
