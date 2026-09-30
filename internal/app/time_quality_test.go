package app

import (
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
	"gnssagent/internal/timequality"
)

func TestTimeQualityRevokedOnSilenceAndRecoveryReconfirms(t *testing.T) {
	h := startAppHarness(t, slog.LevelInfo)
	defer h.stop(t)
	sink := h.sink(t)
	feed := func(epoch int, withGST bool) model.FullStatus {
		stamp := h.startedAt.Add(time.Duration(epoch) * time.Second)
		sink.Sentence(nmea.Sentence{Kind: nmea.KindRMC, ReceivedAt: stamp, RMC: &nmea.RMC{
			MillisOfDay: int64(epoch) * 1000, TimeValid: true,
			Date:   nmea.Field[time.Time]{Value: h.startedAt, Valid: true},
			Status: nmea.Field[byte]{Value: 'A', Valid: true},
		}})
		if withGST {
			sink.Sentence(nmea.Sentence{Kind: nmea.KindGST, ReceivedAt: stamp, GST: &nmea.GST{
				MillisOfDay: int64(epoch) * 1000, TimeValid: true, PseudorangeRMS: nmea.Field[float64]{Value: 1, Valid: true},
			}})
		}
		h.flushTimer.ch <- stamp.Add(aggregateFlushDelay)
		return receiveStatus(t, h.server.published)
	}
	for i := 0; i < 10; i++ {
		status := feed(i, true)
		if i == 9 && status.TimeQuality.State != uint8(timequality.Trusted) {
			t.Fatalf("quality=%+v", status.TimeQuality)
		}
	}
	h.ticker.ch <- h.startedAt.Add(12 * time.Second)
	waitFor(t, func() bool { return h.app.summaryIntervals() >= 1 })
	assertNoStatus(t, h.server.published)
	h.ticker.ch <- h.startedAt.Add(13 * time.Second)
	waitFor(t, func() bool { return h.app.summaryIntervals() >= 2 })
	assertNoStatus(t, h.server.published)
	if recovered := feed(14, true); recovered.TimeQuality.State == uint8(timequality.Trusted) {
		t.Fatal("recovery inherited trusted state")
	}
	if missing := feed(15, false); missing.TimeQuality.State != uint8(timequality.Unknown) || missing.TimeQuality.RMSValid {
		t.Fatalf("no GST=%+v", missing.TimeQuality)
	}
}

func TestQueuedTrustedStatusIsDowngradedAfterExpiry(t *testing.T) {
	gate := make(chan struct{})
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	var elapsed atomic.Int64
	h := startAppHarnessWithGate(t, slog.LevelInfo, gate, func() time.Time {
		return base.Add(time.Duration(elapsed.Load()))
	})
	sink := h.sink(t)
	for i := 0; i < 10; i++ {
		elapsed.Store(int64(time.Duration(i) * time.Second))
		stamp := base.Add(time.Duration(i) * time.Second)
		sink.Sentence(nmea.Sentence{Kind: nmea.KindRMC, ReceivedAt: stamp, RMC: &nmea.RMC{
			MillisOfDay: int64(i) * 1000, TimeValid: true,
			Date:   nmea.Field[time.Time]{Value: base, Valid: true},
			Status: nmea.Field[byte]{Value: 'A', Valid: true},
		}})
		sink.Sentence(nmea.Sentence{Kind: nmea.KindGST, ReceivedAt: stamp, GST: &nmea.GST{
			MillisOfDay: int64(i) * 1000, TimeValid: true, PseudorangeRMS: nmea.Field[float64]{Value: 1, Valid: true},
		}})
		h.app.onFlushDeadline(stamp.Add(aggregateFlushDelay))
		if i == 0 {
			select {
			case <-h.server.publishEntered:
			case <-time.After(time.Second):
				close(gate)
				h.stop(t)
				t.Fatal("publisher did not block")
			}
		}
	}
	elapsed.Store(int64(20 * time.Second))
	h.app.onTick(base.Add(20 * time.Second))
	close(gate)
	defer h.stop(t)
	for i := 0; i < 10; i++ {
		status := receiveStatus(t, h.server.published)
		if status.TimeQuality.State == uint8(timequality.Trusted) {
			t.Fatalf("expired status published as Trusted: %+v", status.TimeQuality)
		}
		if i > 0 && (status.TimeQuality.Reason != uint8(timequality.Stale) || status.TimeQuality.RMSValid || status.TimeQuality.Samples != 0) {
			t.Fatalf("expired queue entry retained evidence: %+v", status.TimeQuality)
		}
	}
}

func TestTimeQualityInputResetRevokesWithoutNavigationFrame(t *testing.T) {
	h := startAppHarness(t, slog.LevelInfo)
	defer h.stop(t)
	sink := h.sink(t)
	sink.Sentence(ggaSentence(h.startedAt, 0, 1))
	h.flushTimer.ch <- h.startedAt.Add(aggregateFlushDelay)
	_ = receiveStatus(t, h.server.published)
	sink.Reset()
	assertNoStatus(t, h.server.published)
}
