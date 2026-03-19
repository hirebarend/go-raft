# go-raft

A Raft consensus implementation in Go, following [Ongaro's paper](https://raft.github.io/raft.pdf).

## Features

- **Leader election** with PreVote optimization (prevents disruptive elections from partitioned nodes)
- **Log replication** with conflict optimization (conflictIndex/conflictTerm for fast backtracking)
- **Snapshotting** — InstallSnapshot RPC, periodic (30s) + manual trigger, two-phase non-blocking writes
- **Crash recovery** from snapshot + WAL replay on startup
- **Pluggable FSM interface** (`Apply`, `Snapshot`, `Restore`)
- **Client request deduplication** via session table (clientId + sequenceNumber, per paper section 8)
- **Leader lease** — leader steps down when it loses majority acknowledgment
- **Configurable parameters** — election timeout, heartbeat interval, batch size, snapshot threshold
- **Graceful shutdown** — drains pending proposals, steps down leader, HTTP server drain with 5s timeout
- **CRC32 integrity checks** on snapshots (V2 format) and log entries
- **Panic recovery** in all background goroutines (applier, AppendEntries, InstallSnapshot, voting)
- **Proposal forwarding** — followers and candidates forward client proposals to the current leader

## Quick Start

```bash
go build .
chmod +x ./scripts/start-cluster.sh
./scripts/start-cluster.sh
```

Propose a value (any node — non-leaders forward to the leader):

```bash
curl -s -X POST http://localhost:8082/propose | jq
```

Check health:

```bash
curl -s http://localhost:8081/ping | jq
```

Trigger a manual snapshot:

```bash
curl -s -X POST http://localhost:8081/snapshot | jq
```

## CLI Flags

| Flag    | Default                                       | Description                                              |
|---------|-----------------------------------------------|----------------------------------------------------------|
| `-bind-host` | `127.0.0.1`                              | Host/IP on which the HTTP server listens                 |
| `-data` | `data`                                        | Path to directory for write-ahead log and persistent state |
| `-nodes`| `127.0.0.1:8081,127.0.0.1:8082,127.0.0.1:8083` | Comma-separated list of cluster peer addresses (host:port) |
| `-advertise-host` | empty                               | Host/IP advertised to peers; defaults to `-bind-host`   |
| `-port` | `8081`                                        | Port for client and peer HTTP requests                   |

## HTTP API

### Client Endpoints

| Method | Path        | Description                              |
|--------|-------------|------------------------------------------|
| POST   | `/propose`  | Propose a value (generates a UUID as the entry data) |
| POST   | `/snapshot` | Trigger a manual snapshot                |
| GET    | `/ping`     | Health check, returns `{"message":"pong"}` |
| GET    | `/disable`  | Disable this node (stops participating in Raft) |
| GET    | `/enable`   | Re-enable a disabled node                |

### Internal RPC (peer-to-peer)

| Method | Path                    | Description                                   |
|--------|-------------------------|-----------------------------------------------|
| POST   | `/rpc/append-entries`   | AppendEntries RPC (heartbeats + log replication) |
| POST   | `/rpc/request-vote`     | RequestVote RPC                               |
| POST   | `/rpc/pre-vote`         | PreVote RPC                                   |
| POST   | `/rpc/install-snapshot` | InstallSnapshot RPC                           |
| POST   | `/rpc/propose`          | Proposal forwarding (follower -> leader)      |

## Architecture

```
Client --> [any node] --forward if not leader--> [Leader]
                                                    |
                                          AppendEntries RPC
                                                    |
                                          [Follower nodes]
```

**Role-based state machine:** each node transitions between Follower, Candidate, and Leader roles. Each role is a separate struct implementing the `RaftRole` interface.

**Transport:** HTTP/JSON over a tuned `http.Client` (300ms dial timeout, connection pooling, keep-alive).

**Write-ahead log:** powered by [go-log](https://github.com/hirebarend/go-log) — a segmented, append-only log with 64 MiB segment size.

**Persistent store:** `store.data` holds `currentTerm` and `votedFor`, written atomically via write-to-temp + fsync + rename.

**Snapshot format (V2):**

```
[version: 1 byte (0x02)]
[lastIncludedIndex: uint64 LE]
[lastIncludedTerm: uint64 LE]
[dataLength: uint64 LE]
[data: variable]
[crc32: uint32 LE (IEEE, over data)]
```

Backwards-compatible: V1 snapshots (no version byte, uint32 data length, no CRC) are detected and read automatically.

## Configuration

The `Config` struct in `internal/config.go` controls Raft timing and limits:

```go
type Config struct {
    TickInterval            time.Duration // Base tick duration (default: 350ms)
    HeartbeatTicks          int           // Ticks between heartbeats (default: 4)
    ElectionTimeoutMinTicks int           // Min ticks before election (default: 12)
    ElectionTimeoutMaxTicks int           // Max ticks before election (default: 20)
    MaxBatchEntries         int           // Max entries per AppendEntries RPC (default: 2048)
    SnapshotThreshold       uint64        // Entries since last snapshot before taking a new one (default: 1000)
    MaxSnapshotSize         uint64        // Maximum snapshot size in bytes (default: 256 MiB)
}
```

**Derived timing** (at default tick interval of 350ms):
- Heartbeat interval: 350ms x 4 = **1.4s**
- Election timeout range: 350ms x 12..20 = **4.2s — 7.0s**

**Validation rules:**
- `HeartbeatTicks` must be less than `ElectionTimeoutMinTicks / 2`
- `ElectionTimeoutMaxTicks` must be >= `ElectionTimeoutMinTicks`
- All values must be positive

## Implementing a Custom FSM

Implement the `FSM` interface:

```go
type FSM interface {
    Apply(data []byte) any
    Snapshot() ([]byte, error)
    Restore(data []byte) error
}
```

- **Apply** is called when a committed log entry is applied to the state machine. The return value is sent back to the proposing client.
- **Snapshot** must return a serialized representation of the full FSM state.
- **Restore** replaces the FSM state from a snapshot. Called on startup recovery and when receiving InstallSnapshot from the leader.

The included `CounterFSM` (backed by the `counter` package) demonstrates a minimal implementation — it counts entries bucketed by time-of-day.

To use a custom FSM, replace the `internal.NewFSM()` call in `main.go` with your own constructor.

## Project Structure

```
.
├── main.go                      # CLI flags, HTTP routes, startup/shutdown
├── internal/
│   ├── config.go                # Config struct, defaults, validation
│   ├── fsm.go                   # FSM interface + CounterFSM implementation
│   ├── raft.go                  # Raft core: state, public API, role transitions
│   ├── follower_role.go         # Follower behavior (election timer, log consistency)
│   ├── candidate_role.go        # Candidate behavior (PreVote + election)
│   ├── leader_role.go           # Leader behavior (replication, commit, lease, applier)
│   ├── log.go                   # Log helper functions (last index/term queries)
│   ├── log_entry.go             # LogEntry serialization (legacy + session-aware + CRC)
│   ├── session.go               # SessionTable for client request deduplication
│   ├── snapshot.go              # Snapshot save/load (V1 + V2 with CRC32)
│   ├── snapshot_test.go         # Snapshot round-trip tests
│   ├── store.go                 # Persistent state (currentTerm, votedFor)
│   └── transport.go             # HTTP/JSON RPC client + request/response types
├── counter/
│   ├── counter.go               # Counter data structure (used by CounterFSM)
│   └── counter_test.go          # Counter tests
├── artillery/
│   └── artillery.yaml           # Load testing config
├── scripts/
│   └── start-cluster.sh         # Cluster launcher script
├── go.mod
├── go.sum
└── LICENSE
```

## Running a Cluster

The `start-cluster.sh` script handles building the node list and process lifecycle:

```bash
./scripts/start-cluster.sh                  # 5 nodes on ports 8081-8085
./scripts/start-cluster.sh -n 3             # 3 nodes
./scripts/start-cluster.sh -n 3 -p 9000    # 3 nodes starting at port 9000
./scripts/start-cluster.sh -d /tmp/raft     # Custom data directory root
```

| Flag | Default     | Description                                  |
|------|-------------|----------------------------------------------|
| `-n` | `5`         | Number of nodes                              |
| `-p` | `8081`      | Base port (nodes increment by 1)             |
| `-H` | `127.0.0.1` | Host/IP for the `--nodes` list               |
| `-d` | `data`      | Root data directory (nodes use `data/data-N`) |
| `-b` | `./go-raft` | Path to the compiled binary                  |

To start individual nodes manually:

```bash
go build -o go-raft .

./go-raft -data data/data-1 -port 8081 -nodes "127.0.0.1:8081,127.0.0.1:8082,127.0.0.1:8083"
./go-raft -data data/data-2 -port 8082 -nodes "127.0.0.1:8081,127.0.0.1:8082,127.0.0.1:8083"
./go-raft -data data/data-3 -port 8083 -nodes "127.0.0.1:8081,127.0.0.1:8082,127.0.0.1:8083"
```

For a non-local deployment, bind the HTTP server to all interfaces but advertise the private node IP:

```bash
./go-raft \
  -data /var/lib/go-raft \
  -bind-host 0.0.0.0 \
  -advertise-host 10.42.0.10 \
  -port 8081 \
  -nodes "10.42.0.10:8081,10.42.0.11:8081,10.42.0.12:8081"
```

Press Ctrl-C to gracefully shut down. The script sends SIGTERM first, then SIGKILL after 2 seconds if needed.

## Load Testing

An [Artillery](https://www.artillery.io/) config is provided:

```bash
npx artillery run artillery/artillery.yaml
```

## DigitalOcean Benchmarking

The repository also includes an end-to-end benchmark harness that provisions 3 raft droplets plus a dedicated Artillery runner, executes the benchmark, downloads artifacts, and destroys the resources afterwards.

Prerequisites:

- `terraform`
- `go`
- `ssh` and `scp`
- a DigitalOcean API token
- an existing DigitalOcean SSH key fingerprint or ID

Required environment variables:

```bash
export DO_TOKEN=...
export DO_SSH_KEY_FINGERPRINT=...
```

Optional environment variables:

```bash
export OPERATOR_CIDR="203.0.113.10/32"   # auto-detected if omitted
export DO_REGION="nyc3"
export RAFT_DROPLET_SIZE="s-1vcpu-1gb"
export ARTILLERY_RUNNER_SIZE="s-1vcpu-1gb"
```

Run the workflow:

```bash
chmod +x ./scripts/deploy-and-benchmark.sh
./scripts/deploy-and-benchmark.sh
```

Artifacts are written to `benchmark-results/<timestamp>/` and include the Artillery JSON report, the Artillery log, the streamed benchmark stdout, and recent `journalctl` output from each raft node.

This ramps from 5 to 20 requests/second over 20 seconds, targeting `POST /propose` on port 8082.

## Safety Properties

The following Raft safety guarantees are implemented:

- **Election safety** — at most one leader per term (single vote per term, persisted)
- **Leader append-only** — leader never overwrites or deletes its own log entries
- **Log matching** — if two logs contain an entry with the same index and term, all preceding entries are identical
- **Leader completeness** — committed entries are present in all future leaders' logs (up-to-date check in RequestVote)
- **State machine safety** — entries are applied in log order, only after commit

Additional safety mechanisms:

- **PreVote** — prevents disruptive elections from partitioned nodes rejoining the cluster
- **Leader lease** — leader rejects proposals and steps down if it hasn't heard from a majority within the election timeout
- **Client deduplication** — prevents duplicate application of retried client requests
- **CRC32 checksums** — detects corruption in snapshots and log entries
- **Atomic persistence** — store writes use write + fsync + rename to prevent partial writes

## Known Limitations

- **No TLS/authentication** on inter-node or client communication
- **No dynamic membership changes** — the node list is static, set at startup
- **No linearizable reads** — read-index / read-lease not implemented; all reads go through the log
- **Single mutex** serializes all Raft state access (throughput ceiling under high concurrency)
- **No log compaction beyond snapshots** — old WAL segments are truncated but the compaction strategy is basic
- **Limited test coverage** for core protocol logic

## License

MIT License. Copyright (c) 2025 Barend Erasmus. See [LICENSE](LICENSE).
