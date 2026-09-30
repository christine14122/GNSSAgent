package timequality

import (
	"math"
	"testing"
	"time"
)

func sample(second int, rms float64) Sample {
	return Sample{UTC: 1_700_006_399_000 + uint64(second)*1000, UTCValid: true, NavigationValid: true, RMS: rms, RMSValid: true, ReceivedAt: time.Unix(1_700_006_399+int64(second), 0)}
}

func trustedTracker(t *testing.T) *Tracker {
	t.Helper()
	tr := New(DefaultConfig())
	for i := 0; i < 10; i++ {
		r := tr.Update(sample(i, 1))
		if i < 9 && r.State == Trusted {
			t.Fatalf("trusted before complete window: %+v", r)
		}
		if i == 9 && (r.State != Trusted || r.Reason != Converged || r.Samples != 10) {
			t.Fatalf("complete stable window: %+v", r)
		}
	}
	return tr
}

func TestStableWindowAndMidnight(t *testing.T) {
	trustedTracker(t)
	tr := New(DefaultConfig())
	for i := 0; i < 10; i++ {
		if got := tr.Update(sample(i, 0)); i == 9 && got.State != Trusted {
			t.Fatalf("zero RMS is valid: %+v", got)
		}
	}
}

func TestWindowMustBeLowAndStable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []float64
		reason Reason
	}{
		{"high", []float64{3, 3, 3, 3, 3, 3, 3, 3, 3, 3}, HighRMS},
		{"unstable", []float64{0, 2, 0, 2, 0, 2, 0, 2, 0, 2}, Unstable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := New(DefaultConfig())
			var r Result
			for i, rms := range tc.values {
				r = tr.Update(sample(i, rms))
			}
			if r.State != Untrusted || r.Reason != tc.reason {
				t.Fatalf("got %+v", r)
			}
		})
	}
	tr := New(DefaultConfig())
	for i := 0; i < 10; i++ {
		rms := float64(i%2) + 1
		if got := tr.Update(sample(i, rms)); i == 9 && got.State != Trusted {
			t.Fatalf("inclusive entry/range bounds: %+v", got)
		}
	}
}

func TestHysteresisAndRecovery(t *testing.T) {
	tr := trustedTracker(t)
	for i, rms := range []float64{6, 6, 5, 6, 6} {
		if got := tr.Update(sample(10+i, rms)); got.State != Trusted {
			t.Fatalf("premature exit at %d: %+v", i, got)
		}
	}
	if got := tr.Update(sample(15, 6)); got.State != Untrusted || got.Reason != HighRMS {
		t.Fatalf("must exit after three consecutive high samples: %+v", got)
	}
	for i := 16; i < 26; i++ {
		got := tr.Update(sample(i, 1))
		if i < 25 && got.State == Trusted {
			t.Fatalf("recovery needs new full window: %+v", got)
		}
		if i == 25 && got.State != Trusted {
			t.Fatalf("did not recover: %+v", got)
		}
	}
}

func TestHardGatesDiscardTrust(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Sample)
		reason Reason
	}{
		{"missing", func(s *Sample) { s.RMSValid = false }, NoGST},
		{"negative", func(s *Sample) { s.RMS = -1 }, InvalidGST},
		{"nan", func(s *Sample) { s.RMS = math.NaN() }, InvalidGST},
		{"infinity", func(s *Sample) { s.RMS = math.Inf(1) }, InvalidGST},
		{"utc", func(s *Sample) { s.UTCValid = false }, InvalidUTC},
		{"navigation", func(s *Sample) { s.NavigationValid = false }, InvalidNavigation},
		{"conflict", func(s *Sample) { s.Conflict = true }, TimeConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := trustedTracker(t)
			s := sample(9, 1) // even bad duplicates must discard trust
			tc.change(&s)
			got := tr.Update(s)
			if got.State == Trusted || got.Reason != tc.reason || got.Samples != 0 {
				t.Fatalf("got %+v", got)
			}
			for i := 10; i < 19; i++ {
				if got := tr.Update(sample(i, 1)); got.State == Trusted {
					t.Fatalf("hard gate did not clear window: %+v", got)
				}
			}
		})
	}
}

func TestDuplicateDoesNotCountOrRefresh(t *testing.T) {
	tr := New(DefaultConfig())
	first := sample(0, 1)
	tr.Update(first)
	duplicate := first
	duplicate.UTC += 500
	duplicate.ReceivedAt = first.ReceivedAt.Add(2 * time.Second)
	if got := tr.Update(duplicate); got.Samples != 1 || got.State == Trusted {
		t.Fatalf("counted duplicate: %+v", got)
	}
	if got, changed := tr.Expire(first.ReceivedAt.Add(3 * time.Second)); !changed || got.Reason != Stale || got.State == Trusted {
		t.Fatalf("duplicate refreshed freshness: %+v changed=%v", got, changed)
	}
	if _, changed := tr.Expire(first.ReceivedAt.Add(4 * time.Second)); changed {
		t.Fatal("repeated stale notification")
	}
	duplicate.ReceivedAt = first.ReceivedAt.Add(5 * time.Second)
	if got := tr.Update(duplicate); got.Reason != Stale || got.Samples != 0 {
		t.Fatalf("stale duplicate restarted freshness: %+v", got)
	}
	tr = New(DefaultConfig())
	tr.Update(first)
	if got := tr.Update(duplicate); got.Reason != Stale || got.Samples != 0 {
		t.Fatalf("duplicate at timeout restarted freshness: %+v", got)
	}
}

func TestExpirationAndGapRequireFreshWindow(t *testing.T) {
	tr := trustedTracker(t)
	if _, changed := tr.Expire(sample(11, 1).ReceivedAt.Add(999 * time.Millisecond)); changed {
		t.Fatal("expired early")
	}
	if got, changed := tr.Expire(sample(12, 1).ReceivedAt); !changed || got.Reason != Stale || got.Samples != 0 {
		t.Fatalf("timeout boundary: %+v %v", got, changed)
	}
	if got := tr.Update(sample(13, 1)); got.State == Trusted || got.Samples != 1 {
		t.Fatalf("stale trust carried forward: %+v", got)
	}
	tr = trustedTracker(t)
	if got := tr.Update(sample(12, 1)); got.State == Trusted || got.Reason != Stale || got.Samples != 1 {
		t.Fatalf("gap not detected: %+v", got)
	}
	if got, changed := tr.Expire(sample(15, 1).ReceivedAt); !changed || got.State != Unknown || got.Samples != 0 {
		t.Fatalf("first sample after a gap must also expire: %+v %v", got, changed)
	}
	if _, changed := New(DefaultConfig()).Expire(time.Now()); changed {
		t.Fatal("never received any data")
	}
}

func TestTimeJumpAndMissingSecond(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Sample)
	}{
		{"backwards", func(s *Sample) { s.UTC -= 2000 }},
		{"within_second_backwards", func(s *Sample) { s.UTC -= 1001 }},
		{"forward", func(s *Sample) { s.UTC += 1000 }},
		{"receive_backwards", func(s *Sample) { s.ReceivedAt = s.ReceivedAt.Add(-2 * time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := trustedTracker(t)
			s := sample(10, 1)
			tc.change(&s)
			if got := tr.Update(s); got.State == Trusted || got.Reason != TimeJump || got.Samples != 0 {
				t.Fatalf("got %+v", got)
			}
		})
	}
	tr := New(DefaultConfig())
	for i := 0; i < 9; i++ {
		tr.Update(sample(i, 1))
	}
	if got := tr.Update(sample(10, 1)); got.State == Trusted || got.Samples != 1 {
		t.Fatalf("missing second must restart window: %+v", got)
	}
}

func TestStepErrorBoundaryAndReset(t *testing.T) {
	tr := New(DefaultConfig())
	tr.Update(sample(0, 1))
	s := sample(1, 1)
	s.ReceivedAt = s.ReceivedAt.Add(500 * time.Millisecond)
	if got := tr.Update(s); got.Reason == TimeJump || got.Samples != 2 {
		t.Fatalf("inclusive step boundary: %+v", got)
	}
	tr.Reset()
	if _, changed := tr.Expire(time.Now()); changed {
		t.Fatal("reset kept freshness")
	}
	if got := tr.Update(sample(0, 1)); got.State != Warming || got.Samples != 1 {
		t.Fatalf("reset: %+v", got)
	}
}

func TestConfigValidation(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, modify := range []func(*Config){
		func(c *Config) { c.EnterRMS = -1 }, func(c *Config) { c.EnterRMS = math.NaN() },
		func(c *Config) { c.ExitRMS = math.Inf(1) }, func(c *Config) { c.ExitRMS = c.EnterRMS },
		func(c *Config) { c.StableRange = -1 }, func(c *Config) { c.StableRange = math.NaN() },
		func(c *Config) { c.Window = 1 }, func(c *Config) { c.Window = 121 },
		func(c *Config) { c.ExitSamples = 0 }, func(c *Config) { c.ExitSamples = 121 },
		func(c *Config) { c.Timeout = 0 }, func(c *Config) { c.Timeout = time.Millisecond - 1 },
		func(c *Config) { c.Timeout = (time.Duration(math.MaxUint32) + 1) * time.Millisecond },
		func(c *Config) { c.MaxTimeStepError = 0 },
	} {
		c := DefaultConfig()
		modify(&c)
		if c.Validate() == nil {
			t.Fatalf("accepted invalid config: %+v", c)
		}
	}
	c := DefaultConfig()
	c.EnterRMS = 0
	c.StableRange = 0
	c.Window = 2
	c.ExitSamples = 1
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, timeout := range []time.Duration{time.Millisecond, time.Duration(math.MaxUint32) * time.Millisecond} {
		c.Timeout = timeout
		if err := c.Validate(); err != nil {
			t.Fatalf("timeout bound %s: %v", timeout, err)
		}
	}
}
