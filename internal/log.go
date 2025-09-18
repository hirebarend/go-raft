package internal

type LogEntry struct {
	Index uint64

	Term uint64
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
