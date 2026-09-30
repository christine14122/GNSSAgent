package aggregate

import (
	"testing"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
	"gnssagent/internal/timequality"
)

func qualityCycle(t *testing.T, a *Aggregator, stamp time.Time, rms ...float64) model.FullStatus {
	t.Helper()
	millis := int64(stamp.Hour()*3600+stamp.Minute()*60+stamp.Second()) * 1000
	rmc := rmcSentence(stamp, millis, true)
	rmc.RMC.Date = field(time.Date(stamp.Year(), stamp.Month(), stamp.Day(), 0, 0, 0, 0, time.UTC))
	a.Add(rmc)
	for _, value := range rms {
		a.Add(nmea.Sentence{Kind: nmea.KindGST, ReceivedAt: stamp.Add(10 * time.Millisecond), GST: &nmea.GST{
			MillisOfDay: millis, TimeValid: true, PseudorangeRMS: field(value),
		}})
	}
	status, ok := a.FlushExpired(stamp.Add(flushDelay))
	if !ok {
		t.Fatal("cycle did not flush")
	}
	return status
}

func TestTimeQualityConvergesOncePerEpochAndExpires(t *testing.T) {
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	a := New()
	for i := 0; i < 10; i++ {
		status := qualityCycle(t, a, base.Add(time.Duration(i)*time.Second), 1, 1)
		want := uint8(timequality.Warming)
		if i == 9 {
			want = uint8(timequality.Trusted)
		}
		if status.TimeQuality.State != want || status.TimeQuality.Samples != uint16(i+1) {
			t.Fatalf("cycle %d quality=%+v", i, status.TimeQuality)
		}
	}
	if _, ok := a.ExpireTimeQuality(base.Add(11 * time.Second)); ok {
		t.Fatal("expired early")
	}
	stale, ok := a.ExpireTimeQuality(base.Add(12 * time.Second))
	if !ok || stale.TimeQuality.State == uint8(timequality.Trusted) || stale.TimeQuality.Reason != uint8(timequality.Stale) {
		t.Fatalf("expiry = %+v, %v", stale.TimeQuality, ok)
	}
	if _, ok := a.ExpireTimeQuality(base.Add(13 * time.Second)); ok {
		t.Fatal("repeated expiry notification")
	}
	status := qualityCycle(t, a, base.Add(13*time.Second), 1)
	if status.TimeQuality.State == uint8(timequality.Trusted) {
		t.Fatal("resumed stream reused convergence")
	}
}

func TestDuplicateEpochPreservesTimeQualityDeadline(t *testing.T) {
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	cfg := timequality.DefaultConfig()
	cfg.Timeout = 10 * time.Second
	a := New(cfg)
	for i := 0; i < 10; i++ {
		qualityCycle(t, a, base.Add(time.Duration(i)*time.Second), 1)
	}
	wantDeadline := base.Add(19 * time.Second)
	late := rmcSentence(base.Add(14*time.Second), 43_209_000, true)
	late.RMC.Date = field(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	a.Add(late)
	a.Add(nmea.Sentence{Kind: nmea.KindGST, ReceivedAt: late.ReceivedAt, GST: &nmea.GST{
		MillisOfDay: 43_209_000, TimeValid: true, PseudorangeRMS: field(1.0),
	}})
	status, ready := a.FlushExpired(late.ReceivedAt.Add(flushDelay))
	if !ready || status.TimeQuality.State != uint8(timequality.Trusted) {
		t.Fatalf("duplicate cycle=%+v, %v", status.TimeQuality, ready)
	}
	if !status.TimeQuality.ExpiresAt.Equal(wantDeadline) {
		t.Fatalf("duplicate extended deadline: %v, want %v", status.TimeQuality.ExpiresAt, wantDeadline)
	}
	if !status.TimeQuality.Expire(wantDeadline) || status.TimeQuality.State == uint8(timequality.Trusted) {
		t.Fatal("queued duplicate outlived original sample")
	}
}

func TestTimeQualityUsesWorstAlignedGSTWithoutChangingRawField(t *testing.T) {
	a := New()
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	status := qualityCycle(t, a, base, 1, 6)
	if status.GSTPseudorangeRMS != 1 || status.TimeQuality.RMS != 6 || !status.TimeQuality.RMSValid {
		t.Fatalf("raw and quality RMS = %v, %+v", status.GSTPseudorangeRMS, status.TimeQuality)
	}
	if status.TimeQuality.State == uint8(timequality.Trusted) {
		t.Fatal("high RMS became trusted")
	}
	status = qualityCycle(t, a, base.Add(time.Second))
	if status.TimeQuality.RMSValid || status.TimeQuality.State != uint8(timequality.Unknown) {
		t.Fatalf("missing GST = %+v", status.TimeQuality)
	}
}

func TestTimeQualityRejectsDateConflictAndUntimedGST(t *testing.T) {
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, conflict := range []bool{false, true} {
		a := New()
		rmc := rmcSentence(base, 43_200_000, true)
		rmc.RMC.Date = field(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
		a.Add(rmc)
		a.Add(nmea.Sentence{Kind: nmea.KindGST, ReceivedAt: base, GST: &nmea.GST{
			MillisOfDay: 43_200_000, TimeValid: conflict, PseudorangeRMS: field(1.0),
		}})
		if conflict {
			a.Add(nmea.Sentence{Kind: nmea.KindZDA, ReceivedAt: base, ZDA: &nmea.ZDA{
				MillisOfDay: 43_200_000, TimeValid: true, Date: field(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
			}})
		}
		status, _ := a.FlushExpired(base.Add(flushDelay))
		want := uint8(timequality.InvalidGST)
		if conflict {
			want = uint8(timequality.TimeConflict)
		}
		if status.TimeQuality.Reason != want || status.TimeQuality.State == uint8(timequality.Trusted) {
			t.Fatalf("conflict=%v: %+v", conflict, status.TimeQuality)
		}
	}
}

func TestRejectedBackwardUTCRevokesTrustImmediately(t *testing.T) {
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	a := New()
	for i := 0; i < 10; i++ {
		qualityCycle(t, a, base.Add(time.Duration(i)*time.Second), 1)
	}
	backward := rmcSentence(base.Add(10*time.Second), 43_205_000, true)
	backward.RMC.Date = field(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if _, published := a.Add(backward); published {
		t.Fatal("late input published navigation")
	}
	quality := a.lastQuality
	if quality.TimeQuality.State != uint8(timequality.Untrusted) || quality.TimeQuality.Reason != uint8(timequality.TimeJump) {
		t.Fatalf("backward quality=%+v", quality.TimeQuality)
	}
}

func TestLateNavigationWarningClearsConvergenceWindow(t *testing.T) {
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	date := field(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	for _, kind := range []nmea.Kind{nmea.KindRMC, nmea.KindGGA, nmea.KindGLL} {
		for _, current := range []bool{false, true} {
			a := New()
			for i := 0; i < 10; i++ {
				qualityCycle(t, a, base.Add(time.Duration(i)*time.Second), 1)
			}
			if current {
				next := rmcSentence(base.Add(10*time.Second), 43_210_000, true)
				next.RMC.Date = date
				a.Add(next)
			}
			late := nmea.Sentence{Kind: kind, ReceivedAt: base.Add(10*time.Second + 5*time.Millisecond)}
			switch kind {
			case nmea.KindRMC:
				late.RMC = &nmea.RMC{MillisOfDay: 43_209_000, TimeValid: true, Date: date, Status: field(byte('V'))}
			case nmea.KindGGA:
				late.GGA = &nmea.GGA{MillisOfDay: 43_209_000, TimeValid: true, Quality: field(uint8(0))}
			case nmea.KindGLL:
				late.GLL = &nmea.GLL{MillisOfDay: 43_209_000, TimeValid: true, Status: field(byte('V'))}
			}
			if _, published := a.Add(late); published {
				t.Fatal("late warning published a navigation cycle")
			}
			if a.lastQuality.TimeQuality.State != uint8(timequality.Untrusted) || a.lastQuality.TimeQuality.Reason != uint8(timequality.InvalidNavigation) {
				t.Errorf("kind=%v current=%v retained trust: %+v", kind, current, a.lastQuality.TimeQuality)
			}
			status := qualityCycle(t, a, base.Add(10*time.Second), 1)
			if status.TimeQuality.State == uint8(timequality.Trusted) || status.TimeQuality.Samples != 0 || status.TimeQuality.Reason != uint8(timequality.InvalidNavigation) {
				t.Errorf("kind=%v current=%v next cycle retained old window: %+v", kind, current, status.TimeQuality)
			}
			for i := 11; i <= 20; i++ {
				status = qualityCycle(t, a, base.Add(time.Duration(i)*time.Second), 1)
				if i < 20 && status.TimeQuality.State == uint8(timequality.Trusted) {
					t.Fatalf("kind=%v recovered before 10 new cycles", kind)
				}
			}
			if status.TimeQuality.State != uint8(timequality.Trusted) {
				t.Fatalf("kind=%v did not recover after 10 new cycles", kind)
			}
		}
	}
}

func TestSubsecondUTCConflictCannotConverge(t *testing.T) {
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	a := New()
	for i := 0; i < 10; i++ {
		stamp := base.Add(time.Duration(i) * time.Second)
		millis := int64(43_200+i) * 1000
		rmc := rmcSentence(stamp, millis, true)
		rmc.RMC.Date = field(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
		a.Add(rmc)
		a.Add(nmea.Sentence{Kind: nmea.KindZDA, ReceivedAt: stamp, ZDA: &nmea.ZDA{MillisOfDay: millis + 900, TimeValid: true, Date: rmc.RMC.Date}})
		a.Add(nmea.Sentence{Kind: nmea.KindGST, ReceivedAt: stamp, GST: &nmea.GST{MillisOfDay: millis, TimeValid: true, PseudorangeRMS: field(1.0)}})
		status, _ := a.FlushExpired(stamp.Add(flushDelay))
		if status.TimeQuality.Reason != uint8(timequality.TimeConflict) {
			t.Fatalf("cycle %d: %+v", i, status.TimeQuality)
		}
	}
}

func TestLateSameSecondConflictRevokesTrust(t *testing.T) {
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	a := New()
	for i := 0; i < 10; i++ {
		qualityCycle(t, a, base.Add(time.Duration(i)*time.Second), 1)
	}
	late := nmea.Sentence{Kind: nmea.KindZDA, ReceivedAt: base.Add(10 * time.Second), ZDA: &nmea.ZDA{
		MillisOfDay: 43_209_000, TimeValid: true, Date: field(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)),
	}}
	a.Add(late)
	if a.lastQuality.TimeQuality.State != uint8(timequality.Trusted) {
		t.Fatal("exact duplicate revoked trust")
	}
	late.ZDA.MillisOfDay += 900
	a.Add(late)
	quality := a.lastQuality
	if quality.TimeQuality.State != uint8(timequality.Untrusted) {
		t.Fatalf("late conflict=%+v", quality.TimeQuality)
	}
}

func TestTimeQualityChecksFullUTCSpreadInAnyOrder(t *testing.T) {
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	date := field(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	for _, test := range []struct {
		name     string
		offsets  [3]int64
		conflict bool
	}{
		{"999ms spread", [3]int64{500, 0, 999}, true},
		{"500ms spread", [3]int64{250, 0, 500}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, order := range []struct {
				name    string
				indices [3]int
			}{
				{"middle-min-max", [3]int{0, 1, 2}},
				{"middle-max-min", [3]int{0, 2, 1}},
				{"min-middle-max", [3]int{1, 0, 2}},
				{"min-max-middle", [3]int{1, 2, 0}},
				{"max-middle-min", [3]int{2, 0, 1}},
				{"max-min-middle", [3]int{2, 1, 0}},
			} {
				t.Run(order.name, func(t *testing.T) {
					a := New()
					for i := 0; i < 10; i++ {
						stamp := base.Add(time.Duration(i) * time.Second)
						millis := int64(43_200+i) * 1000
						rmc := rmcSentence(stamp, millis+test.offsets[0], true)
						rmc.RMC.Date = date
						sources := [3]nmea.Sentence{
							rmc,
							{Kind: nmea.KindZDA, ReceivedAt: stamp, ZDA: &nmea.ZDA{MillisOfDay: millis + test.offsets[1], TimeValid: true, Date: date}},
							{Kind: nmea.KindZDA, ReceivedAt: stamp, ZDA: &nmea.ZDA{MillisOfDay: millis + test.offsets[2], TimeValid: true, Date: date}},
						}
						for _, index := range order.indices {
							a.Add(sources[index])
						}
						firstOffset := test.offsets[order.indices[0]]
						a.Add(nmea.Sentence{Kind: nmea.KindGST, ReceivedAt: stamp, GST: &nmea.GST{
							MillisOfDay: millis + firstOffset, TimeValid: true, PseudorangeRMS: field(1.0),
						}})
						status, ok := a.FlushExpired(stamp.Add(flushDelay))
						if !ok {
							t.Fatal("cycle did not flush")
						}
						if status.UTCTime != uint64(stamp.UnixMilli()+firstOffset) {
							t.Fatalf("cycle %d changed first raw UTC: %d", i, status.UTCTime)
						}
						if test.conflict {
							if status.TimeQuality.State == uint8(timequality.Trusted) || status.TimeQuality.Reason != uint8(timequality.TimeConflict) {
								t.Errorf("cycle %d accepted conflicting UTC sources: %+v", i, status.TimeQuality)
							}
						} else if i == 9 && status.TimeQuality.State != uint8(timequality.Trusted) {
							t.Fatalf("500ms UTC spread did not converge: %+v", status.TimeQuality)
						}
					}
				})
			}
		})
	}
}
