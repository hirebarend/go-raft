package internal

type Log struct {
	logEntries []LogEntry
}

func (l *Log) Append(logEntries []LogEntry) error {
	var lastIndex uint64

	if len(l.logEntries) > 0 {
		lastIndex = l.logEntries[len(l.logEntries)-1].Index
	}

	for i := range logEntries {
		lastIndex++

		logEntries[i].Index = lastIndex

		l.logEntries = append(l.logEntries, logEntries[i])
	}

	return nil
}

func (l *Log) Get(index uint64) (LogEntry, bool) {
	if len(l.logEntries) == 0 {
		return LogEntry{}, false
	}

	lo, hi := 0, len(l.logEntries)-1

	for lo <= hi {
		mid := (lo + hi) / 2
		logEntry := l.logEntries[mid]

		if logEntry.Index == index {
			return logEntry, true
		}

		if logEntry.Index < index {
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
	if len(l.logEntries) == 0 {
		return LogEntry{}, false
	}

	return l.logEntries[len(l.logEntries)-1], true
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
	if len(l.logEntries) == 0 {
		return
	}

	position := -1

	for i, logEntry := range l.logEntries {
		if logEntry.Index == index {
			position = i
			break
		}
	}

	if position == -1 {
		return
	}

	l.logEntries = l.logEntries[:position]
}
