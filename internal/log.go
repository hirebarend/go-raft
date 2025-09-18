package internal

type LogEntry struct {
	Index uint64
	Term  uint64
}

type Log struct {
	entries []LogEntry
}

func (l *Log) Append(entries []LogEntry) error {
	var lastIndex uint64

	if len(l.entries) > 0 {
		lastIndex = l.entries[len(l.entries)-1].Index
	}

	for i := range entries {
		lastIndex++

		entries[i].Index = lastIndex

		l.entries = append(l.entries, entries[i])
	}

	return nil
}

func (l *Log) Get(index uint64) (LogEntry, bool) {
	if len(l.entries) == 0 {
		return LogEntry{}, false
	}

	lo, hi := 0, len(l.entries)-1

	for lo <= hi {
		mid := (lo + hi) / 2
		e := l.entries[mid]

		if e.Index == index {
			return e, true
		}

		if e.Index < index {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}

	return LogEntry{}, false
}

func (l *Log) GetTerm(index uint64) (uint64, bool) {
	logEntry, ok := l.Get(index)

	if !ok {
		return 0, false
	}

	return logEntry.Term, true
}

func (l *Log) Last() (LogEntry, bool) {
	if len(l.entries) == 0 {
		return LogEntry{}, false
	}

	return l.entries[len(l.entries)-1], true
}

func (l *Log) LastIndex() (uint64, bool) {
	logEntry, ok := l.Last()

	if !ok {
		return 0, false
	}

	return logEntry.Index, true
}

func (l *Log) LastTerm() (uint64, bool) {
	logEntry, ok := l.Last()

	if !ok {
		return 0, false
	}

	return logEntry.Term, true
}

func (l *Log) Truncate(index uint64) {
	if len(l.entries) == 0 {
		return
	}

	position := -1

	for i, e := range l.entries {
		if e.Index == index {
			position = i
			break
		}
	}

	if position == -1 {
		return
	}

	l.entries = l.entries[:position]
}
