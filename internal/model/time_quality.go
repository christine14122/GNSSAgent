package model

import "time"

// TimeQuality carries the GST RMS heuristic result, not an absolute time accuracy.
// State and Reason use the values defined by internal/timequality.
type TimeQuality struct {
	Evaluated     bool
	State         uint8
	Reason        uint8
	Samples       uint16
	RMSValid      bool
	RMS           float32
	TimeoutMillis uint32
	// ExpiresAt retains the local monotonic clock across publication queues.
	// It is internal metadata and is never encoded in the wire protocol.
	ExpiresAt time.Time
}

// Expire prevents a queued snapshot from becoming fresh again when transmitted.
// The return value reports whether the internal deadline has elapsed.
func (q *TimeQuality) Expire(now time.Time) bool {
	if !q.Evaluated || q.ExpiresAt.IsZero() || now.Before(q.ExpiresAt) {
		return false
	}
	q.State, q.Reason, q.Samples = 0, 9, 0 // UNKNOWN / STALE
	q.RMSValid, q.RMS = false, 0
	return true
}
