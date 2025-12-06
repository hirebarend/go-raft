package internal

const (
	// Election timeout is the time a follower waits until it assumes the leader has crashed and starts an election.
	// This is a randomized value to prevent split votes.
	electionTimeoutMin = 150
	electionTimeoutMax = 300

	// Heartbeat interval is the time a leader waits between sending heartbeats to its followers.
	// This should be less than the election timeout to prevent followers from starting unnecessary elections.
	heartbeatInterval = 50
)
