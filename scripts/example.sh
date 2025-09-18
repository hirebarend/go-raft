#!/usr/bin/env bash
set -euo pipefail

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

./raft-go --port 8081 --nodes 127.0.0.1:8081,127.0.0.1:8082,127.0.0.1:8083 & pids+=($!)
./raft-go --port 8082 --nodes 127.0.0.1:8081,127.0.0.1:8082,127.0.0.1:8083 & pids+=($!)
./raft-go --port 8083 --nodes 127.0.0.1:8081,127.0.0.1:8082,127.0.0.1:8083 & pids+=($!)

echo "Cluster started. Press Ctrl-C to stop."

wait "${pids[@]}"
