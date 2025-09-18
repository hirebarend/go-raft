package internal

type LogEntry struct {
	Index uint64

	Term uint64
}

type Log struct {
	entries []LogEntry
}

func (l *Log) Append() {

}
