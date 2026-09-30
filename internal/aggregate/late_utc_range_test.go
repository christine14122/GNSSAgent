package aggregate

import (
	"testing"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
	"gnssagent/internal/timequality"
)

func TestLateUTCRangeIncludesPublishedAndLateSources(t *testing.T) {
	for _, test := range []struct {
		name     string
		original []int64
		late     []int64
		conflict bool
	}{
		{"published minimum", []int64{500, 0}, []int64{999}, true},
		{"published maximum", []int64{500, 999}, []int64{0}, true},
		{"late minimum then maximum", []int64{500}, []int64{0, 999}, true},
		{"late maximum then minimum", []int64{500}, []int64{999, 0}, true},
		{"exact 500ms spread", []int64{250}, []int64{0, 500, 250, 0}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, path := range []string{"flushed", "current timed", "current untimed"} {
				t.Run(path, func(t *testing.T) {
					epoch := time.Date(2026, 9, 30, 12, 0, 9, 0, time.UTC)
					a, before := trustedUTCRange(t, epoch, test.original...)
					nextReceived := epoch.Add(flushDelay)
					switch path {
					case "current timed":
						a.Add(utcRangeSentence(nextReceived, epoch.Add(1500*time.Millisecond), false))
					case "current untimed":
						a.Add(rmcSentence(nextReceived, 0, false))
					}
					for i, offset := range test.late {
						late := utcRangeSentence(epoch.Add(time.Duration(1600+i*10)*time.Millisecond), epoch.Add(time.Duration(offset)*time.Millisecond), i%2 != 0)
						if _, published := a.Add(late); published {
							t.Fatal("late UTC published a navigation cycle")
						}
						if a.lastQuality.UTCTime != before.UTCTime {
							t.Fatalf("late UTC replaced raw UTC: got %d, want %d", a.lastQuality.UTCTime, before.UTCTime)
						}
						if test.conflict && i == len(test.late)-1 {
							assertUTCRangeConflict(t, a.lastQuality.TimeQuality)
						} else {
							assertUTCRangeUnchanged(t, a.lastQuality.TimeQuality, before.TimeQuality)
						}
					}
					if test.conflict {
						status := utcRangeCycle(t, a, epoch.Add(2*time.Second), epoch.Add(time.Second), 500)
						assertUTCRangeConflict(t, status.TimeQuality)
					} else {
						expired, changed := a.ExpireTimeQuality(before.TimeQuality.ExpiresAt)
						if !changed || expired.TimeQuality.Reason != uint8(timequality.Stale) {
							t.Fatalf("late boundary inputs renewed trust: %+v, changed=%v", expired.TimeQuality, changed)
						}
					}
				})
			}
		})
	}
}

func TestLateUTCRangeSurvivesCompletedGuardExpiry(t *testing.T) {
	for _, test := range []struct {
		name       string
		original   []int64
		beforeLate []int64
		repeated   []int64
		afterLate  []int64
	}{
		{"published minimum conflicts on reaggregation", []int64{500, 0}, nil, []int64{500, 999}, nil},
		{"late minimum conflicts on reaggregation", []int64{500}, []int64{0}, []int64{500, 999}, nil},
		{"late maximum conflicts on reaggregation", []int64{500}, []int64{999}, []int64{500, 0}, nil},
		{"compatible reaggregation preserves published minimum", []int64{500, 0}, nil, []int64{500}, []int64{999}},
		{"compatible reaggregation preserves late minimum", []int64{500}, []int64{0}, []int64{500}, []int64{999}},
	} {
		t.Run(test.name, func(t *testing.T) {
			epoch := time.Date(2026, 9, 30, 12, 0, 9, 0, time.UTC)
			a, before := trustedUTCRange(t, epoch, test.original...)
			for _, offset := range test.beforeLate {
				a.Add(utcRangeSentence(epoch.Add(1600*time.Millisecond), epoch.Add(time.Duration(offset)*time.Millisecond), true))
			}
			assertUTCRangeUnchanged(t, a.lastQuality.TimeQuality, before.TimeQuality)
			// The previous cycle flushed at +1.5s. At +5s the 3s arrival
			// guard has expired, but the original 10s quality lease has not.
			received := epoch.Add(5 * time.Second)
			status := utcRangeCycle(t, a, received, epoch, test.repeated...)
			if len(test.afterLate) == 0 {
				assertUTCRangeConflict(t, status.TimeQuality)
				return
			}
			assertUTCRangeUnchanged(t, status.TimeQuality, before.TimeQuality)
			for _, offset := range test.afterLate {
				if _, published := a.Add(utcRangeSentence(received.Add(1600*time.Millisecond), epoch.Add(time.Duration(offset)*time.Millisecond), false)); published {
					t.Fatal("late UTC published after reaggregation")
				}
			}
			assertUTCRangeConflict(t, a.lastQuality.TimeQuality)
		})
	}
}

func TestLateUTCRangeDoesNotLeakAfterClear(t *testing.T) {
	epoch := time.Date(2026, 9, 30, 12, 0, 9, 0, time.UTC)
	a, _ := trustedUTCRange(t, epoch, 500, 0)
	a.Clear()
	status := utcRangeCycle(t, a, epoch.Add(2*time.Second), epoch, 999)
	if status.TimeQuality.State != uint8(timequality.Warming) || status.TimeQuality.Samples != 1 {
		t.Fatalf("Clear retained prior UTC evidence: %+v", status.TimeQuality)
	}
	a.Add(utcRangeSentence(epoch.Add(3600*time.Millisecond), epoch.Add(499*time.Millisecond), true))
	assertUTCRangeUnchanged(t, a.lastQuality.TimeQuality, status.TimeQuality)
}

func TestLateUTCRangeDoesNotLeakIntoNextUTCSecond(t *testing.T) {
	for _, epoch := range []time.Time{
		time.Date(2026, 9, 30, 12, 0, 9, 0, time.UTC),
		time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC),
	} {
		t.Run(epoch.Format(time.RFC3339), func(t *testing.T) {
			a, before := trustedUTCRange(t, epoch, 500, 0)
			nextEpoch := epoch.Add(time.Second)
			received := epoch.Add(flushDelay)
			status := utcRangeCycle(t, a, received, nextEpoch, 999)
			if status.TimeQuality.State != uint8(timequality.Trusted) || !status.TimeQuality.ExpiresAt.After(before.TimeQuality.ExpiresAt) {
				t.Fatalf("new UTC second inherited prior range: %+v", status.TimeQuality)
			}
			if status.UTCTime != uint64(nextEpoch.Add(999*time.Millisecond).UnixMilli()) {
				t.Fatalf("new cycle published wrong full UTC: %d", status.UTCTime)
			}
			a.Add(utcRangeSentence(received.Add(1600*time.Millisecond), nextEpoch.Add(499*time.Millisecond), true))
			assertUTCRangeUnchanged(t, a.lastQuality.TimeQuality, status.TimeQuality)
		})
	}
}

func trustedUTCRange(t *testing.T, epoch time.Time, offsets ...int64) (*Aggregator, model.FullStatus) {
	t.Helper()
	cfg := timequality.DefaultConfig()
	cfg.Timeout = 10 * time.Second
	a := New(cfg)
	for i := 9; i > 0; i-- {
		qualityCycle(t, a, epoch.Add(-time.Duration(i)*time.Second), 1)
	}
	status := utcRangeCycle(t, a, epoch, epoch, offsets...)
	if status.TimeQuality.State != uint8(timequality.Trusted) || status.TimeQuality.Samples != 10 {
		t.Fatalf("initial cycle did not converge: %+v", status.TimeQuality)
	}
	return a, status
}

func utcRangeCycle(t *testing.T, a *Aggregator, received, epoch time.Time, offsets ...int64) model.FullStatus {
	t.Helper()
	for i, offset := range offsets {
		a.Add(utcRangeSentence(received, epoch.Add(time.Duration(offset)*time.Millisecond), i > 0))
	}
	first := epoch.Add(time.Duration(offsets[0]) * time.Millisecond)
	a.Add(nmea.Sentence{Kind: nmea.KindGST, ReceivedAt: received, GST: &nmea.GST{
		MillisOfDay: first.UnixMilli() % 86_400_000, TimeValid: true, PseudorangeRMS: field(1.0),
	}})
	status, published := a.FlushExpired(received.Add(flushDelay))
	if !published {
		t.Fatal("UTC range cycle did not flush")
	}
	return status
}

func utcRangeSentence(received, utc time.Time, zda bool) nmea.Sentence {
	date := field(time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC))
	millis := utc.UnixMilli() % 86_400_000
	if zda {
		return nmea.Sentence{Kind: nmea.KindZDA, ReceivedAt: received, ZDA: &nmea.ZDA{
			MillisOfDay: millis, TimeValid: true, Date: date,
		}}
	}
	sentence := rmcSentence(received, millis, true)
	sentence.RMC.Date = date
	return sentence
}

func assertUTCRangeConflict(t *testing.T, quality model.TimeQuality) {
	t.Helper()
	if quality.State != uint8(timequality.Untrusted) || quality.Reason != uint8(timequality.TimeConflict) || quality.Samples != 0 || !quality.ExpiresAt.IsZero() {
		t.Fatalf("conflicting UTC evidence retained trust: %+v", quality)
	}
}

func assertUTCRangeUnchanged(t *testing.T, got, want model.TimeQuality) {
	t.Helper()
	if got.State != want.State || got.Reason != want.Reason || got.Samples != want.Samples || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("compatible late UTC changed quality: got %+v, want %+v", got, want)
	}
}
