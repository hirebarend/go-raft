package internal

type LogEntry struct {
	Data  []byte
	Index uint64
	Term  uint64
}
