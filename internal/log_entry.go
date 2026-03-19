package internal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

type LogEntry struct {
	Data           []byte
	Term           uint64
	ClientId       string
	SequenceNumber uint64
}

// Serialize encodes the log entry into binary format.
// Format: [Term:8][ClientIdLen:4][ClientId:var][SeqNum:8][Data:var]
// For backward compat, if ClientId is empty, uses the legacy format: [Term:8][Data:var]
func (e *LogEntry) Serialize() []byte {
	if e.ClientId == "" && e.SequenceNumber == 0 {
		// Legacy format
		buf := make([]byte, 8+len(e.Data))
		binary.LittleEndian.PutUint64(buf[:8], e.Term)
		copy(buf[8:], e.Data)
		return buf
	}

	clientIdBytes := []byte(e.ClientId)
	size := 8 + 4 + len(clientIdBytes) + 8 + len(e.Data)
	buf := make([]byte, size)
	offset := 0

	binary.LittleEndian.PutUint64(buf[offset:], e.Term)
	offset += 8

	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(clientIdBytes)))
	offset += 4

	copy(buf[offset:], clientIdBytes)
	offset += len(clientIdBytes)

	binary.LittleEndian.PutUint64(buf[offset:], e.SequenceNumber)
	offset += 8

	copy(buf[offset:], e.Data)

	return buf
}

// DeserializeLogEntry decodes a binary log entry.
// It handles both legacy format (just term + data) and new format (with client session info).
// Distinguishing heuristic: if len >= 20 (8 term + 4 clientIdLen + 8 seqNum minimum)
// and the clientIdLen field produces a valid parse, use new format.
func DeserializeLogEntry(data []byte) (*LogEntry, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("log entry too short: %d bytes", len(data))
	}

	term := binary.LittleEndian.Uint64(data[:8])

	// Try new format: need at least 8 (term) + 4 (clientIdLen) = 12 bytes
	if len(data) >= 12 {
		clientIdLen := binary.LittleEndian.Uint32(data[8:12])
		// Validate: clientIdLen must be reasonable, and total must fit
		requiredLen := 8 + 4 + int(clientIdLen) + 8
		if clientIdLen > 0 && clientIdLen <= 1024 && len(data) >= requiredLen {
			clientId := string(data[12 : 12+clientIdLen])
			seqNum := binary.LittleEndian.Uint64(data[12+clientIdLen : 12+clientIdLen+8])
			payload := data[requiredLen:]

			return &LogEntry{
				Term:           term,
				ClientId:       clientId,
				SequenceNumber: seqNum,
				Data:           payload,
			}, nil
		}
	}

	// Legacy format
	payload := data[8:]
	return &LogEntry{
		Term: term,
		Data: payload,
	}, nil
}

// SerializeWithCRC serializes the log entry and appends a CRC32 checksum.
func (e *LogEntry) SerializeWithCRC() []byte {
	payload := e.Serialize()
	crc := crc32.ChecksumIEEE(payload)
	buf := make([]byte, len(payload)+4)
	copy(buf, payload)
	binary.LittleEndian.PutUint32(buf[len(payload):], crc)
	return buf
}

// DeserializeLogEntryWithCRC decodes a log entry with CRC32 integrity check.
func DeserializeLogEntryWithCRC(data []byte) (*LogEntry, error) {
	if len(data) < 12 { // at least 8 (term) + 4 (crc)
		return nil, fmt.Errorf("log entry with CRC too short: %d bytes", len(data))
	}

	payload := data[:len(data)-4]
	expected := binary.LittleEndian.Uint32(data[len(data)-4:])
	actual := crc32.ChecksumIEEE(payload)

	if expected != actual {
		return nil, fmt.Errorf("log entry CRC mismatch: expected %08x, got %08x", expected, actual)
	}

	return DeserializeLogEntry(payload)
}
