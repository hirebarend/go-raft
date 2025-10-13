#!/usr/bin/env bash
set -euo pipefail

# Defaults (override via flags)
NUM_NODES=5
BASE_PORT=8081
HOST=127.0.0.1
DATA_ROOT="data"
BIN="./go-raft"

usage() {
  cat <<EOF
Usage: $(basename "$0") [-n NUM_NODES] [-p BASE_PORT] [-H HOST] [-d DATA_ROOT] [-b BIN]
  -n  Number of nodes to start (default: ${NUM_NODES})
  -p  Base TCP port for first node; others increment by 1 (default: ${BASE_PORT})
  -H  Host/IP used in --nodes (default: ${HOST})
  -d  Root data directory; nodes use {DATA_ROOT}/data-1..N (default: ${DATA_ROOT})
  -b  Path to raft-go binary (default: ${BIN})
EOF
}

while getopts ":n:p:H:d:b:h" opt; do
  case "$opt" in
    n) NUM_NODES="${OPTARG}" ;;
    p) BASE_PORT="${OPTARG}" ;;
    H) HOST="${OPTARG}" ;;
    d) DATA_ROOT="${OPTARG}" ;;
    b) BIN="${OPTARG}" ;;
    h) usage; exit 0 ;;
    \?) echo "Invalid option: -$OPTARG" >&2; usage; exit 1 ;;
  esac
done

# Basic validation
[[ "$NUM_NODES" =~ ^[0-9]+$ ]] || { echo "NUM_NODES must be an integer"; exit 1; }
[[ "$BASE_PORT" =~ ^[0-9]+$ ]] || { echo "BASE_PORT must be an integer"; exit 1; }
[[ -x "$BIN" ]] || { echo "Binary not executable: $BIN"; exit 1; }

# Build nodes list like "127.0.0.1:8081,127.0.0.1:8082,..."
nodes_csv=""
for ((i=0; i<NUM_NODES; i++)); do
  port=$((BASE_PORT + i))
  entry="${HOST}:${port}"
  if [[ -z "$nodes_csv" ]]; then
    nodes_csv="$entry"
  else
    nodes_csv="${nodes_csv},${entry}"
  fi
done
NODES_CSV="$nodes_csv"

pids=()

cleanup() {
  echo ""
  echo "Shutting down cluster..."
  for pid in "${pids[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  sleep 2
  for pid in "${pids[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      echo "Force killing PID $pid"
      kill -9 "$pid" 2>/dev/null || true
    fi
  done
}

trap cleanup EXIT INT TERM

# Start all nodes
for ((i=1; i<=NUM_NODES; i++)); do
  port=$((BASE_PORT + i - 1))
  data_dir="${DATA_ROOT}/data-${i}"
  mkdir -p "$data_dir"

  # Start and capture PID (no negative array indexing)
  "$BIN" --data "$data_dir" --port "$port" --nodes "$NODES_CSV" & pid=$!
  pids+=("$pid")

  echo "Started node ${i}/${NUM_NODES}: port ${port}, pid ${pid}"
done

echo "Cluster started (${NUM_NODES} nodes on ${HOST}:${BASE_PORT}..$((BASE_PORT+NUM_NODES-1))). Press Ctrl-C to stop."

# Wait for all children
wait "${pids[@]}"
