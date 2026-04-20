# Raft Implementation Gap Analysis

A detailed comparison of the `go-raft` implementation against the Raft specification (Ongaro & Ousterhout, "In Search of an Understandable Consensus Algorithm, Extended Version").

---

## Section 1 — Gaps Between Existing Features and the Specification

These are features that **are** implemented in `go-raft`, but where the implementation **deviates** from or **does not fully satisfy** the specification.

### 1.1 InstallSnapshot RPC: Missing Chunked Transfer (§7, Figure 13)

**Specification:** The InstallSnapshot RPC is designed to send snapshots in **chunks**. The RPC includes `offset` (byte offset where the chunk is positioned) and `done` (whether this is the last chunk) fields. The receiver should create a new snapshot file when `offset` is 0, write data at the given offset, and wait for more chunks until `done` is true. This chunked approach also allows the follower to reset its election timer with each chunk, preventing election timeouts during large snapshot transfers.

**Implementation:** The `InstallSnapshot` RPC sends the **entire snapshot in a single request** (`transport.go`). The `InstallSnapshotRequest` struct has no `offset` or `done` fields. The follower processes the full snapshot atomically in one call. For small snapshots this works, but for large snapshots (up to the configured 256 MiB maximum), this can cause HTTP timeouts and follower election timeouts during the transfer because there is no incremental "sign of life."

**Gap:** No chunked snapshot transfer; missing `offset` and `done` fields; follower cannot reset election timer incrementally during snapshot receipt.

---

### 1.2 AppendEntries: commitIndex Update Rule (§5.3, Figure 2)

**Specification:** "If leaderCommit > commitIndex, set commitIndex = min(leaderCommit, index of last new entry)."

**Implementation:** In `follower_role.go`, `HandleAppendEntries` calls `r.raft.setCommitIndex(leaderCommitIndex)`, which caps the commit index at the last log entry index but does **not** cap it at the index of the last *new* entry received in this specific RPC. If the leader sends `leaderCommitIndex` that is well beyond what the follower has received so far, the follower will set its commitIndex to its last log entry — which may include entries not validated by the current AppendEntries consistency check.

**Gap:** The follower should compute `min(leaderCommit, prevLogIndex + len(entries))` and use that as the upper bound, not just the last log entry index.

---

### 1.3 Follower Redirect Behaviour (§8)

**Specification:** "If the client's first choice is not the leader, that server will reject the client's request and supply information about the most recent leader it has heard from."

**Implementation:** In `follower_role.go` and `candidate_role.go`, `HandlePropose` does **not** reject with leader information returned to the external client. Instead, it **transparently forwards** the proposal to the leader via `transport.Propose()`. While forwarding is a valid design choice, the `/propose` HTTP endpoint does not return the leader's address to the client on error — it returns a generic `"bad request"` or `"no known leader"` error without the leader's identity.

**Gap:** Clients receive no leader address information on failure, preventing them from reconnecting directly. The specification expects the server to supply the leader's address.

---

### 1.4 Persistent State: votedFor Must Be Persisted Before Responding to RequestVote (§5, Figure 2)

**Specification:** "Updated on stable storage before responding to RPCs" — `currentTerm` and `votedFor` must be persisted *before* the RPC response is sent.

**Implementation:** In `follower_role.go`, `HandleRequestVote` does persist `votedFor` via `store.SetVotedFor()` before returning the response, and `SetCurrentTermAndVotedFor` is called in `OnEnter`. However, in `HandleAppendEntries`, when `term > currentTerm`, the method calls `becomeFollower(term)` which triggers `OnEnter` → `SetCurrentTermAndVotedFor`. This writes to disk *inside* the same call path before the response is sent, which is correct. The store uses write-to-temp + fsync + rename, which is sound. **However**, `IncrementCurrentTerm` in `candidate_role.go` writes term and votedFor in two separate disk operations (`IncrementCurrentTerm` then `SetVotedFor`). If the process crashes between these two writes, the node could have incremented its term but not yet recorded its self-vote, which could lead to voting for a different candidate in the same term after restart.

**Gap:** `startElection()` should atomically persist both the incremented term and self-vote in a single write operation.

---

### 1.5 Log Entries: Term Retained From Original Leader (§5.4.2)

**Specification:** "Log entries retain their original term numbers when a leader replicates entries from previous terms."

**Implementation:** This is correctly implemented — entries replicated to followers keep their original term. However, the commit advancement logic in `tryToAdvanceCommitIndex` correctly checks `logEntry.Term == currentTerm` (matching the specification's rule that leaders only commit entries from their current term by counting replicas). **The gap is subtle**: the no-op entry appended on leader election (`OnEnter` in `leader_role.go`) is correct, but it is written to the local log and replicated *before* `tryToAdvanceCommitIndex` is called. If the leader has *only* entries from previous terms and the no-op has not yet been replicated to a majority, the leader cannot advance the commit index at all — which is correct per spec. **No functional gap**, but the implementation does not make this safety invariant explicit through comments or assertions.

**Gap:** Minor — the critical safety invariant (§5.4.2) that prevents committing old-term entries by replica count is implicitly upheld but not documented in code.

---

### 1.6 Candidate Handling of AppendEntries With Equal Term (§5.2)

**Specification:** "If a candidate receives an AppendEntries RPC from another server claiming to be leader... If the leader's term is at least as large as the candidate's current term, then the candidate recognizes the leader as legitimate and returns to follower state."

**Implementation:** In `candidate_role.go`, `HandleAppendEntries` checks `if term < currentTerm` and rejects, otherwise converts to follower. This means for `term == currentTerm`, the candidate correctly steps down. This matches the spec. **No gap.**

---

### 1.7 Snapshot Metadata: Missing Cluster Configuration (§7)

**Specification:** "To enable cluster membership changes (Section 6), the snapshot also includes the latest configuration in the log as of last included index."

**Implementation:** The `Snapshot` struct (`snapshot.go`) stores only `LastIncludedIndex`, `LastIncludedTerm`, and `Data`. There is no cluster configuration metadata in the snapshot. While this is partially justified because dynamic membership changes are not implemented (see Section 2), even the static configuration should be captured in the snapshot for completeness and forward compatibility.

**Gap:** Snapshot does not include cluster configuration metadata.

---

### 1.8 Log Matching Property: Follower Entry Overwrite (§5.3, Figure 2 Rule 3-4)

**Specification:** "If an existing entry conflicts with a new one (same index but different terms), delete the existing entry and all that follow it" and "Append any new entries not already in the log."

**Implementation:** In `follower_role.go`, `appendEntries` iterates through received entries, checks if an existing entry at the same index has a conflicting term, and if so, truncates from that point and writes the remaining entries. If terms match, it skips. If the read fails (entry doesn't exist), it writes the remaining entries. This is correct and matches the specification.

**Gap:** None for the core logic. However, the implementation does not handle partial write failures gracefully — if `writeEntries` returns false partway through, the follower has already truncated conflicting entries but may not have written all replacements, leaving the log in an inconsistent state until the next AppendEntries retry.

---

## Section 2 — Features Not Yet Implemented

These are features described in the Raft specification that are **entirely absent** from the `go-raft` implementation.

### 2.1 Dynamic Cluster Membership Changes (§6)

**Specification:** Section 6 describes a mechanism for changing the cluster configuration at runtime using **joint consensus**. The process involves:
1. The leader creates a `C_old,new` configuration entry combining old and new configurations.
2. This entry is replicated using rules of joint consensus (requires separate majorities from both old and new configurations).
3. Once `C_old,new` is committed, the leader creates a `C_new` entry.
4. Once `C_new` is committed, servers not in the new configuration can be shut down.

Additional sub-features:
- **Non-voting members**: New servers join as non-voting members to catch up before becoming full members.
- **Leader step-down**: If the leader is not part of the new configuration, it steps down after committing `C_new`.
- **Disruption prevention**: Servers disregard RequestVote RPCs when they believe a current leader exists (within minimum election timeout of hearing from the leader).

**Implementation:** The node list is static, set at startup via the `-nodes` CLI flag. There is no mechanism to add or remove nodes at runtime. The README explicitly lists "No dynamic membership changes" as a known limitation.

**Impact:** The cluster cannot grow, shrink, or replace failed nodes without a full restart of all nodes with updated configuration.

---

### 2.2 Linearizable Read-Only Operations (§8)

**Specification:** Section 8 describes how read-only operations can be handled without writing to the log, but requires two precautions for linearizability:
1. **No-op on election**: A leader must commit a blank no-op entry at the start of its term to learn which entries are committed (the implementation already does this).
2. **Read-index / Heartbeat check**: Before responding to a read-only request, the leader must exchange heartbeat messages with a majority of the cluster to confirm it is still the leader. Alternatively, the leader can use a lease-based approach, but this relies on timing for safety.

**Implementation:** There is no read-only request path. All operations go through the log via `Propose`. The README lists "No linearizable reads — read-index / read-lease not implemented; all reads go through the log" as a known limitation. The leader lease mechanism exists (for stepping down and rejecting proposals) but is not used to serve reads.

**Impact:** Read operations are unnecessarily expensive (require full log replication round-trip) and the system provides no way to read from the FSM state directly.

---

### 2.3 Leader Transfer (Not in core spec, but related to §6)

**Specification:** While not explicitly part of the core Raft paper, the specification discusses leader step-down in the context of membership changes (§6), where the leader may not be part of the new configuration and must step down. The broader Raft ecosystem (e.g., the Raft dissertation) includes **leadership transfer** as a mechanism where the leader can voluntarily transfer leadership to another server.

**Implementation:** The leader can step down (via lease expiry or `Disable()`), but there is no mechanism to explicitly transfer leadership to a specific node. This would be useful for graceful maintenance and rolling upgrades.

**Impact:** No targeted leadership transfer; leadership change relies on election timeout and is non-deterministic.

---

### 2.4 Client Session Registration and Expiry (§8)

**Specification:** Section 8 describes that "the state machine tracks the latest serial number processed for each client, along with the associated response." This implies clients must **register** sessions and that sessions should have a lifecycle (creation, use, expiry/cleanup).

**Implementation:** The `SessionTable` (`session.go`) tracks client sessions keyed by `clientId` with a `sequenceNumber`, but:
- There is **no session registration mechanism** — any `clientId` is accepted implicitly.
- There is **no session expiry or cleanup** — the session table grows unboundedly.
- Session data is serializable (`Serialize`/`Deserialize`) for snapshots, but the session table is **not included in snapshots** in practice (the `CounterFSM` snapshots only the counter state, not the session table). This means after a snapshot restore, duplicate detection is lost.

**Impact:** The session table leaks memory over time, and session state is not preserved across snapshot boundaries.

---

### 2.5 Formal Handling of No-Op Entries in State Machine Application (§5.3, §8)

**Specification:** The leader commits a **blank no-op entry** at the start of its term (§8). This entry should be applied to the state machine but should not produce any side effects.

**Implementation:** The no-op entry is written with `Data: nil` in `leader_role.go` `OnEnter`. The apply logic (`applyToFiniteStateMachine`) checks `if len(logEntry.Data) > 0` before calling `fsm.Apply()`, so no-op entries are skipped. This is functionally correct. However, the specification suggests the no-op is needed to determine which entries are committed — the implementation does this implicitly through the commit index advancement logic.

**Gap:** None functionally, but the implementation has no explicit concept of a "no-op" entry type — it relies on empty data as a heuristic.

---

### 2.6 Server Retry of RPCs on Failure (§5.1, §5.5)

**Specification:** "Servers retry RPCs if they do not receive a response in a timely manner, and they issue RPCs in parallel for best performance" (§5.1). "Raft handles these failures by retrying indefinitely; if the crashed server restarts, then the RPC will complete successfully" (§5.5).

**Implementation:** For `AppendEntries`, the leader retries on the next heartbeat tick or when a new log entry is proposed. Failed RPCs silently return default values (e.g., `term, false, 0, 0`). There is no explicit retry-with-backoff mechanism for RPCs. The `inflight` flag prevents concurrent RPCs to the same node, but a failed RPC simply clears the flag and waits for the next tick. For `RequestVote`, failed RPCs are fire-and-forget — if the vote request fails, it is never retried for that election round.

**Gap:** RequestVote RPCs are not retried within the same election round. A transient network failure to a single peer can prevent a candidate from gathering a majority even when the peer is actually reachable.

---

### 2.7 Leader Redirection Information in Client Responses (§8)

**Specification:** "If the client's first choice is not the leader, that server will reject the client's request and supply information about the most recent leader it has heard from (AppendEntries requests include the network address of the leader)."

**Implementation:** The HTTP API returns generic error messages (`"bad request"`, `"no known leader"`) without including the leader's address. While the internal Raft state tracks the leader ID (`store.leaderId`), this information is never exposed to external clients in HTTP responses.

**Impact:** Clients have no efficient way to discover the current leader after a leadership change, requiring them to try nodes randomly.

---

### 2.8 Protection Against Disruption by Removed Servers (§6)

**Specification:** Section 6 describes that removed servers can disrupt the cluster by starting elections with higher term numbers. To prevent this: "if a server receives a RequestVote RPC within the minimum election timeout of hearing from a current leader, it does not update its term or grant its vote."

**Implementation:** The PreVote mechanism partially addresses this by preventing partitioned nodes from disrupting the cluster (a node must win a pre-vote before starting a real election). However, the specific protection described in §6 — ignoring RequestVote RPCs when a current leader is known to be alive — is **not implemented**. In `follower_role.go`, `HandleRequestVote` does not check when the last AppendEntries was received from the current leader.

**Gap:** A follower will grant votes even if it recently heard from the leader, which means a partitioned-then-returned node that somehow bypasses PreVote (e.g., if PreVote is not used for some code path) could disrupt the cluster.

---

### 2.9 Configuration Entry as a Special Log Entry Type (§6)

**Specification:** "Cluster configurations are stored and communicated using special entries in the replicated log" (§6). Configuration changes are proposed as log entries and take effect as soon as they are added to a server's log (even before being committed).

**Implementation:** There is no concept of configuration log entries. The `LogEntry` struct has `Data`, `Term`, `ClientId`, and `SequenceNumber` fields — there is no entry type discriminator that would allow distinguishing between client commands and configuration changes.

**Impact:** Cannot implement dynamic membership changes without adding a log entry type system.

---

### 2.10 Copy-on-Write Snapshots (§7)

**Specification:** Section 7 mentions that "writing a snapshot can take a significant amount of time, and we do not want this to delay normal operations. The solution is to use copy-on-write techniques so that new updates can be accepted without impacting the snapshot being written."

**Implementation:** The `TakeSnapshot` method uses a two-phase approach: Phase 1 captures FSM state under the lock, Phase 2 writes to disk outside the lock. This prevents blocking RPCs during the disk write. However, the FSM's `Snapshot()` method is called **under the lock** in Phase 1, which means if `Snapshot()` is slow (e.g., serializing a large state machine), it will block all Raft operations. There is no copy-on-write (e.g., via `fork()` or immutable data structures) to allow the FSM to continue accepting applies while the snapshot is being captured.

**Gap:** FSM snapshot capture blocks all Raft operations for the duration of the serialization.

---

## Summary

| Category | Count |
|---|---|
| Gaps in existing features | 8 (§1.1–§1.8) |
| Features not yet implemented | 10 (§2.1–§2.10) |

### Priority Recommendations

1. **High — Dynamic Membership Changes (§2.1):** Most impactful missing feature for production use.
2. **High — Atomic Term+Vote Persistence (§1.4):** Safety-critical bug that could violate election safety.
3. **High — Chunked InstallSnapshot (§1.1):** Required for reliable snapshot transfer in production.
4. **Medium — commitIndex Update Rule (§1.2):** Subtle correctness issue with commit index advancement.
5. **Medium — Linearizable Reads (§2.2):** Performance improvement for read-heavy workloads.
6. **Medium — Session Lifecycle (§2.4):** Memory leak in long-running clusters.
7. **Medium — RequestVote Retry (§2.6):** Reduces election reliability under transient failures.
8. **Medium — Leader Redirect (§1.3, §2.7):** Better client experience and faster failover.
9. **Low — Copy-on-Write Snapshots (§2.10):** Performance improvement for large state machines.
10. **Low — Snapshot Configuration (§1.7):** Forward compatibility for future membership changes.
