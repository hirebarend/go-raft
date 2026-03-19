package internal

import golog "github.com/hirebarend/go-log"

func GetLastLogEntryIndexAndTerm(log *golog.Log) (uint64, uint64) {
	lastLogEntryIndex, err := log.GetLastIndex()

	if err != nil {
		return 0, 0
	}

	if lastLogEntryIndex == 0 {
		return 0, 0
	}

	data, err := log.Read(lastLogEntryIndex)

	if err != nil {
		return 0, 0
	}

	lastLogEntry, err := DeserializeLogEntry(data)

	if err != nil {
		return 0, 0
	}

	return lastLogEntryIndex, lastLogEntry.Term
}

func GetLastLogEntryIndexOfTerm(log *golog.Log, term uint64) uint64 {
	if term == 0 {
		return 0
	}

	lastLogEntryIndex, err := log.GetLastIndex()

	if err != nil || lastLogEntryIndex == 0 {
		return 0
	}

	for i := lastLogEntryIndex; i > 0; i-- {
		data, err := log.Read(i)

		if err != nil {
			return 0
		}

		logEntry, err := DeserializeLogEntry(data)

		if err != nil || logEntry == nil {
			return 0
		}
		if logEntry.Term == term {
			return i
		}

		if logEntry.Term < term {
			return 0
		}
	}

	return 0
}

func IsEqualOrMoreRecent(log *golog.Log, index, term uint64) bool {
	myLastLogEntryIndex, myLastLogEntryTerm := GetLastLogEntryIndexAndTerm(log)

	if term > myLastLogEntryTerm {
		return true
	}

	if term == myLastLogEntryTerm && index >= myLastLogEntryIndex {
		return true
	}

	return false
}

func LogEntryMatchesTermAtIndex(log *golog.Log, index uint64, term uint64) bool {
	if index == 0 {
		return true
	}

	data, err := log.Read(index)

	if err != nil {
		return false
	}

	prevLogEntry, err := DeserializeLogEntry(data)

	if err != nil {
		return false
	}

	if prevLogEntry.Term != term {
		return false
	}

	return true
}
