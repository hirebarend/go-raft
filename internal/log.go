package internal

type LogEntry struct {
	Index int64

	Term int64
}

type Log struct {
	entries []LogEntry
}

func (l *Log) Append() {

}
