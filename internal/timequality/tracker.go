// Package timequality evaluates an application-specific GST RMS heuristic.
// Trusted means the configured acceptance rules passed, not measured UTC accuracy.
package timequality

import (
	"fmt"
	"math"
	"time"
)

type Config struct {
	EnterRMS         float64
	ExitRMS          float64
	StableRange      float64
	Window           int
	ExitSamples      int
	Timeout          time.Duration
	MaxTimeStepError time.Duration
}

// DefaultConfig contains initial empirical thresholds, not receiver guarantees.
func DefaultConfig() Config {
	return Config{EnterRMS: 2, ExitRMS: 5, StableRange: 1, Window: 10,
		ExitSamples: 3, Timeout: 3 * time.Second, MaxTimeStepError: 500 * time.Millisecond}
}

func (c Config) Validate() error {
	for _, v := range []struct {
		name  string
		value float64
	}{
		{"enter RMS", c.EnterRMS}, {"exit RMS", c.ExitRMS}, {"stable range", c.StableRange},
	} {
		if !finiteNonnegative(v.value) {
			return fmt.Errorf("time quality %s must be finite and nonnegative", v.name)
		}
	}
	if c.ExitRMS <= c.EnterRMS {
		return fmt.Errorf("time quality exit RMS must exceed enter RMS")
	}
	if c.Window < 2 || c.Window > 120 {
		return fmt.Errorf("time quality window must be between 2 and 120")
	}
	if c.ExitSamples < 1 || c.ExitSamples > 120 {
		return fmt.Errorf("time quality exit samples must be between 1 and 120")
	}
	if c.Timeout < time.Millisecond || c.Timeout > time.Duration(math.MaxUint32)*time.Millisecond {
		return fmt.Errorf("time quality timeout must be between 1 and %d milliseconds", uint64(math.MaxUint32))
	}
	if c.MaxTimeStepError <= 0 {
		return fmt.Errorf("time quality maximum time step error must be positive")
	}
	return nil
}

type State uint8

const (
	Unknown State = iota
	Warming
	Trusted
	Untrusted
)

type Reason uint8

const (
	NoGST Reason = iota
	Collecting
	Converged
	HighRMS
	Unstable
	InvalidUTC
	InvalidNavigation
	TimeConflict
	TimeJump
	Stale
	InvalidGST
)

type Sample struct {
	UTC             uint64 // Unix milliseconds; validity is independent of the numeric value.
	UTCValid        bool
	NavigationValid bool
	RMS             float64 // Metres, from GST field 2.
	RMSValid        bool
	Conflict        bool
	ReceivedAt      time.Time // Preserve its monotonic component when available.
}

type Result struct {
	State   State
	Reason  Reason
	Samples uint16
}

// Tracker is owned by the aggregation loop and is not concurrency-safe.
type Tracker struct {
	config       Config
	window       []float64
	highCount    int
	lastUTC      uint64
	lastReceived time.Time
	hasLast      bool
	result       Result
}

// New requires a validated config and panics for programming errors.
func New(c Config) *Tracker {
	if err := c.Validate(); err != nil {
		panic(err)
	}
	return &Tracker{config: c}
}

func (t *Tracker) Reset() {
	t.window = t.window[:0]
	t.highCount = 0
	t.hasLast = false
	t.lastUTC = 0
	t.lastReceived = time.Time{}
	t.result = Result{State: Unknown, Reason: NoGST}
}

func (t *Tracker) invalidate(state State, reason Reason) Result {
	t.Reset()
	t.result = Result{State: state, Reason: reason}
	return t.result
}

// ExpiresAt is tied to the last accepted new epoch, never to a retransmission.
// Keep this deadline with snapshots so queueing cannot renew their lifetime.
func (t *Tracker) ExpiresAt() time.Time {
	if !t.hasLast {
		return time.Time{}
	}
	return t.lastReceived.Add(t.config.Timeout)
}

// Expire revokes stale data even if no further NMEA message arrives.
// A duplicate epoch does not postpone this deadline.
func (t *Tracker) Expire(now time.Time) (Result, bool) {
	if !t.hasLast || t.result.State == Unknown && t.result.Reason == Stale || now.Sub(t.lastReceived) < t.config.Timeout {
		return t.result, false
	}
	// Keep the last epoch so delayed duplicates cannot restart its lifetime.
	t.window = t.window[:0]
	t.highCount = 0
	t.result = Result{State: Unknown, Reason: Stale}
	return t.result, true
}

func (t *Tracker) Update(s Sample) Result {
	// Hard gates also apply to duplicate epochs, so bad data cannot retain trust.
	if s.Conflict {
		return t.invalidate(Untrusted, TimeConflict)
	}
	if !s.UTCValid {
		return t.invalidate(Unknown, InvalidUTC)
	}
	if !s.NavigationValid {
		return t.invalidate(Untrusted, InvalidNavigation)
	}
	if !s.RMSValid {
		return t.invalidate(Unknown, NoGST)
	}
	if !finiteNonnegative(s.RMS) {
		return t.invalidate(Untrusted, InvalidGST)
	}

	stale := false
	if t.hasLast {
		elapsed := s.ReceivedAt.Sub(t.lastReceived)
		if s.UTC < t.lastUTC || elapsed < 0 {
			return t.invalidate(Untrusted, TimeJump)
		}
		if s.UTC/1000 == t.lastUTC/1000 {
			t.Expire(s.ReceivedAt)
			return t.result
		}
		if elapsed >= t.config.Timeout {
			t.invalidate(Unknown, Stale)
			stale = true
		} else {
			milliseconds := s.UTC - t.lastUTC
			if milliseconds > uint64(math.MaxInt64/int64(time.Millisecond)) {
				return t.invalidate(Untrusted, TimeJump)
			}
			step := time.Duration(milliseconds) * time.Millisecond
			if step > elapsed && step-elapsed > t.config.MaxTimeStepError || elapsed > step && elapsed-step > t.config.MaxTimeStepError {
				return t.invalidate(Untrusted, TimeJump)
			}
			if s.UTC/1000 != t.lastUTC/1000+1 {
				// Missing epochs interrupt the continuous observation window.
				t.Reset()
			}
		}
	}
	t.hasLast = true
	t.lastUTC = s.UTC
	t.lastReceived = s.ReceivedAt

	if t.result.State == Trusted {
		if s.RMS > t.config.ExitRMS {
			t.highCount++
		} else {
			t.highCount = 0
		}
		if t.highCount >= t.config.ExitSamples {
			t.window = t.window[:0]
			t.highCount = 0
			t.result = Result{State: Untrusted, Reason: HighRMS}
		}
		return t.result
	}

	if len(t.window) == t.config.Window {
		t.window = append(t.window[:0], t.window[1:]...)
	}
	t.window = append(t.window, s.RMS)
	t.result = Result{State: Warming, Reason: Collecting, Samples: uint16(len(t.window))}
	if stale {
		t.result.Reason = Stale
		return t.result
	}
	min, max := t.window[0], t.window[0]
	for _, rms := range t.window[1:] {
		if rms < min {
			min = rms
		}
		if rms > max {
			max = rms
		}
	}
	if max > t.config.EnterRMS {
		t.result.State, t.result.Reason = Untrusted, HighRMS
	} else if len(t.window) == t.config.Window {
		if max-min > t.config.StableRange {
			t.result.State, t.result.Reason = Untrusted, Unstable
		} else {
			t.result.State, t.result.Reason = Trusted, Converged
		}
	}
	return t.result
}

func finiteNonnegative(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }
