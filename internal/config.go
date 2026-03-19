package internal

import (
	"fmt"
	"time"
)

type Config struct {
	// TickInterval is the base duration of a single Raft tick.
	TickInterval time.Duration

	// HeartbeatTicks is the number of ticks between leader heartbeats.
	HeartbeatTicks int

	// ElectionTimeoutMinTicks is the minimum number of ticks before a follower starts an election.
	ElectionTimeoutMinTicks int

	// ElectionTimeoutMaxTicks is the maximum number of ticks before a follower starts an election.
	ElectionTimeoutMaxTicks int

	// MaxBatchEntries is the maximum number of log entries sent in a single AppendEntries RPC.
	MaxBatchEntries int

	// SnapshotThreshold is the number of log entries beyond the last snapshot before a new snapshot is taken.
	SnapshotThreshold uint64

	// MaxSnapshotSize is the maximum allowed snapshot size in bytes.
	MaxSnapshotSize uint64
}

func DefaultConfig() Config {
	return Config{
		TickInterval:            350 * time.Millisecond,
		HeartbeatTicks:          4,
		ElectionTimeoutMinTicks: 12,
		ElectionTimeoutMaxTicks: 20,
		MaxBatchEntries:         2048,
		SnapshotThreshold:       1000,
		MaxSnapshotSize:         256 << 20, // 256 MiB
	}
}

func (c Config) Validate() error {
	if c.TickInterval <= 0 {
		return fmt.Errorf("TickInterval must be positive")
	}
	if c.HeartbeatTicks <= 0 {
		return fmt.Errorf("HeartbeatTicks must be positive")
	}
	if c.ElectionTimeoutMinTicks <= 0 {
		return fmt.Errorf("ElectionTimeoutMinTicks must be positive")
	}
	if c.ElectionTimeoutMaxTicks < c.ElectionTimeoutMinTicks {
		return fmt.Errorf("ElectionTimeoutMaxTicks must be >= ElectionTimeoutMinTicks")
	}
	if c.HeartbeatTicks >= c.ElectionTimeoutMinTicks/2 {
		return fmt.Errorf("HeartbeatTicks (%d) must be < ElectionTimeoutMinTicks/2 (%d)", c.HeartbeatTicks, c.ElectionTimeoutMinTicks/2)
	}
	if c.MaxBatchEntries <= 0 {
		return fmt.Errorf("MaxBatchEntries must be positive")
	}
	if c.SnapshotThreshold == 0 {
		return fmt.Errorf("SnapshotThreshold must be positive")
	}
	if c.MaxSnapshotSize == 0 {
		return fmt.Errorf("MaxSnapshotSize must be positive")
	}
	return nil
}
