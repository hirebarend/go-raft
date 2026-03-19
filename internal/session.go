package internal

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

// SessionTable tracks the latest sequence number applied per client for deduplication.
type SessionTable struct {
	mu       sync.RWMutex
	sessions map[string]SessionEntry
}

type SessionEntry struct {
	SequenceNumber uint64
	Result         any
}

func NewSessionTable() *SessionTable {
	return &SessionTable{
		sessions: make(map[string]SessionEntry),
	}
}

// IsDuplicate checks if the given client+sequence has already been applied.
// Returns (isDuplicate, cachedResult).
func (st *SessionTable) IsDuplicate(clientId string, sequenceNumber uint64) (bool, any) {
	if clientId == "" {
		return false, nil
	}

	st.mu.RLock()
	defer st.mu.RUnlock()

	entry, ok := st.sessions[clientId]
	if !ok {
		return false, nil
	}

	if sequenceNumber <= entry.SequenceNumber {
		return true, entry.Result
	}

	return false, nil
}

// Record records the result for a client+sequence.
func (st *SessionTable) Record(clientId string, sequenceNumber uint64, result any) {
	if clientId == "" {
		return
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	entry, ok := st.sessions[clientId]
	if !ok || sequenceNumber > entry.SequenceNumber {
		st.sessions[clientId] = SessionEntry{
			SequenceNumber: sequenceNumber,
			Result:         result,
		}
	}
}

// Serialize encodes the session table for inclusion in snapshots.
// Format: [count:4][{clientIdLen:4, clientId:var, seqNum:8}...]
// Note: Result is not serialized (only the sequence number matters for dedup).
func (st *SessionTable) Serialize() []byte {
	st.mu.RLock()
	defer st.mu.RUnlock()

	// Calculate size
	size := 4
	for clientId := range st.sessions {
		size += 4 + len(clientId) + 8
	}

	buf := make([]byte, size)
	offset := 0

	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(st.sessions)))
	offset += 4

	for clientId, entry := range st.sessions {
		cid := []byte(clientId)
		binary.LittleEndian.PutUint32(buf[offset:], uint32(len(cid)))
		offset += 4
		copy(buf[offset:], cid)
		offset += len(cid)
		binary.LittleEndian.PutUint64(buf[offset:], entry.SequenceNumber)
		offset += 8
	}

	return buf
}

// Deserialize restores the session table from a snapshot.
func (st *SessionTable) Deserialize(data []byte) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if len(data) < 4 {
		return fmt.Errorf("session table too short: %d bytes", len(data))
	}

	r := newByteReader(data)

	count, err := r.readUint32()
	if err != nil {
		return err
	}

	st.sessions = make(map[string]SessionEntry, count)

	for i := uint32(0); i < count; i++ {
		cidLen, err := r.readUint32()
		if err != nil {
			return fmt.Errorf("read clientId len [%d]: %w", i, err)
		}
		cid, err := r.readBytes(int(cidLen))
		if err != nil {
			return fmt.Errorf("read clientId [%d]: %w", i, err)
		}
		seqNum, err := r.readUint64()
		if err != nil {
			return fmt.Errorf("read seqNum [%d]: %w", i, err)
		}

		st.sessions[string(cid)] = SessionEntry{
			SequenceNumber: seqNum,
		}
	}

	return nil
}

// byteReader is a simple helper for reading from a byte slice.
type byteReader struct {
	data   []byte
	offset int
}

func newByteReader(data []byte) *byteReader {
	return &byteReader{data: data}
}

func (r *byteReader) readUint32() (uint32, error) {
	if r.offset+4 > len(r.data) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint32(r.data[r.offset:])
	r.offset += 4
	return v, nil
}

func (r *byteReader) readUint64() (uint64, error) {
	if r.offset+8 > len(r.data) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint64(r.data[r.offset:])
	r.offset += 8
	return v, nil
}

func (r *byteReader) readBytes(n int) ([]byte, error) {
	if r.offset+n > len(r.data) {
		return nil, io.ErrUnexpectedEOF
	}
	b := make([]byte, n)
	copy(b, r.data[r.offset:r.offset+n])
	r.offset += n
	return b, nil
}
