package app

import (
	"log/slog"
	"testing"
	"time"

	"gnssagent/internal/model"
)

func TestLateSentenceDoesNotDisarmNextCycleFlush(t *testing.T) {
	base := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	timer := newManualDeadlineTimer()
	a := newApp(nil, nil, nil, nil, newManualTicker(), timer, func() time.Time { return base })
	sink := &inputSink{app: a}
	sink.Sentence(ggaSentence(base, 1, 1))
	a.onFlushDeadline(base.Add(1500 * time.Millisecond))
	if _, ok := a.nextStatus(); !ok {
		t.Fatal("first cycle was not flushed")
	}
	sink.Sentence(ggaSentence(base.Add(1600*time.Millisecond), 1, 1))
	if !a.flushDeadline.IsZero() {
		t.Error("discarded late sentence scheduled a flush")
	}
	sink.Sentence(ggaSentence(base.Add(2*time.Second), 2, 1))
	a.onFlushDeadline(base.Add(3100 * time.Millisecond))
	if _, ok := a.nextStatus(); ok {
		t.Fatal("second cycle flushed before its deadline")
	}
	a.onFlushDeadline(base.Add(3500 * time.Millisecond))
	status, ok := a.nextStatus()
	if !ok || status.RecvTime != uint64(base.Add(2*time.Second).UnixMilli()) {
		t.Fatalf("second cycle was not flushed at its deadline: status=%+v, ready=%v", status, ok)
	}
}

func TestPublisherLimitsBurstAndKeepsLatestStatus(t *testing.T) {
	h := startAppHarness(t, slog.LevelInfo)
	defer h.stop(t)
	h.app.offerStatus(model.FullStatus{UTCTime: 1})
	if got := receiveStatus(t, h.server.published); got.UTCTime != 1 {
		t.Fatalf("first status = %d", got.UTCTime)
	}
	firstPublished := time.Now()
	for epoch := uint64(2); epoch <= 4; epoch++ {
		h.app.offerStatus(model.FullStatus{UTCTime: epoch})
	}
	select {
	case got := <-h.server.published:
		t.Fatalf("burst bypassed publication interval: %+v", got)
	case <-time.After(300 * time.Millisecond):
	}
	got := receiveStatus(t, h.server.published)
	if got.UTCTime != 4 {
		t.Fatalf("pending status = %d, want latest epoch 4", got.UTCTime)
	}
	if elapsed := time.Since(firstPublished); elapsed < 950*time.Millisecond {
		t.Fatalf("publication interval = %v, want approximately one second", elapsed)
	}
	assertNoStatus(t, h.server.published)
}
