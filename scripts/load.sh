curl -X GET http://localhost:8084/profiler

curl -X POST http://localhost:8082/propose \
  -H "Content-Type: application/json" \
  -d '{}'

npx artillery run artillery.yaml






# Enable contention sampling (on-demand)
curl "http://127.0.0.1:6060/debug/pprof/config?mutex=5&block=1000"
#   mutex=5     -> ~20% of lock events sampled (good starting point)
#   block=1000  -> samples goroutine blocking roughly every 1µs spent blocked

# Capture a CPU profile for 30s
go tool pprof -http=:0 "http://127.0.0.1:6060/debug/pprof/profile?seconds=30"

# Capture a Mutex contention profile (after enabling above)
go tool pprof -http=:0 "http://127.0.0.1:6060/debug/pprof/mutex"

# Capture a Block profile (who is waiting on channels/cond/locks)
go tool pprof -http=:0 "http://127.0.0.1:6060/debug/pprof/block"

# Grab a 10s execution trace (great for IO vs CPU vs scheduler)
curl -o trace.out "http://127.0.0.1:6060/debug/pprof/trace?seconds=10"
go tool trace trace.out
