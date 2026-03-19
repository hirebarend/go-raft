package internal

import (
	"encoding/binary"
	"fmt"
)

type LogEntry struct {
	Data []byte
	Term uint64
}

func (e *LogEntry) Serialize() []byte {
	buf := make([]byte, 8+len(e.Data))
	binary.LittleEndian.PutUint64(buf[:8], e.Term)
	copy(buf[8:], e.Data)
	return buf
}

func DeserializeLogEntry(data []byte) (*LogEntry, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("log entry too short: %d bytes", len(data))
	}
	term := binary.LittleEndian.Uint64(data[:8])
	payload := data[8:]
	entry := &LogEntry{
		Term: term,
		Data: payload,
	}
	return entry, nil
}
