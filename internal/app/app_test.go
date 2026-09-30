package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
	"gnssagent/internal/observe"
	"gnssagent/internal/protocol"
	"gnssagent/internal/udpinput"
)

type fakeUDPInput struct {
	started chan udpinput.Sink
	stopped chan struct{}
	runErr  error
}

func newFakeUDPInput() *fakeUDPInput {
	return &fakeUDPInput{started: make(chan udpinput.Sink, 1), stopped: make(chan struct{})}
}

func (f *fakeUDPInput) Run(ctx context.Context, sink udpinput.Sink) error {
	f.started <- sink
	if f.runErr != nil {
		return f.runErr
	}
	<-ctx.Done()
	close(f.stopped)
	return ctx.Err()
}

type fakeStatusServer struct {
	started        chan struct{}
	stopped        chan struct{}
	published      chan model.FullStatus
	publishEntered chan struct{}
	publishGate    <-chan struct{}
	runErr         error
}

func newFakeStatusServer() *fakeStatusServer {
	return &fakeStatusServer{
		started:        make(chan struct{}),
		stopped:        make(chan struct{}),
		published:      make(chan model.FullStatus, 16),
		publishEntered: make(chan struct{}, 16),
	}
}

func (f *fakeStatusServer) Run(ctx context.Context) error {
	close(f.started)
	if f.runErr != nil {
		return f.runErr
	}
	<-ctx.Done()
	close(f.stopped)
	return nil
}

func (f *fakeStatusServer) Publish(status model.FullStatus) {
	select {
	case f.publishEntered <- struct{}{}:
	default:
	}
	if f.publishGate != nil {
		<-f.publishGate
	}
	f.published <- status
}

type manualTicker struct {
	ch      chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func newManualTicker() *manualTicker {
	return &manualTicker{ch: make(chan time.Time, 128), stopped: make(chan struct{})}
}

func (t *manualTicker) C() <-chan time.Time { return t.ch }
func (t *manualTicker) Stop()               { t.once.Do(func() { close(t.stopped) }) }

type manualDeadlineTimer struct {
	ch        chan time.Time
	deadlines chan time.Time
	stops     chan struct{}
}

func newManualDeadlineTimer() *manualDeadlineTimer {
	return &manualDeadlineTimer{
		ch:        make(chan time.Time, 16),
		deadlines: make(chan time.Time, 16),
		stops:     make(chan struct{}, 16),
	}
}

func (t *manualDeadlineTimer) C() <-chan time.Time { return t.ch }
func (t *manualDeadlineTimer) Reset(deadline time.Time) {
	t.deadlines <- deadline
}
func (t *manualDeadlineTimer) Stop() { t.stops <- struct{}{} }

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestRunStartsTCPWhileUDPInputIsRunning(t *testing.T) {
	harness := startAppHarness(t, slog.LevelInfo)
	defer harness.stop(t)
	select {
	case <-harness.server.started:
	case <-time.After(time.Second):
		t.Fatal("TCP server did not start independently")
	}
	select {
	case <-harness.input.started:
	case <-time.After(time.Second):
		t.Fatal("UDP input did not start")
	}
}

func TestSentenceTransitionPublishesAtMostOncePerUTCSecond(t *testing.T) {
	harness := startAppHarness(t, slog.LevelInfo)
	defer harness.stop(t)
	sink := harness.sink(t)
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	sink.Sentence(ggaSentence(base, 1, 1))
	sink.Sentence(ggaSentence(base.Add(100*time.Millisecond), 1, 1))
	assertNoStatus(t, harness.server.published)
	sink.Sentence(ggaSentence(base.Add(time.Second), 2, 1))
	first := receiveStatus(t, harness.server.published)
	if first.FieldValidityMask&model.FullValidValid == 0 || first.Valid != 1 {
		t.Fatalf("published status validity = %#x/%d", first.FieldValidityMask, first.Valid)
	}
	sink.Sentence(ggaSentence(base.Add(1100*time.Millisecond), 2, 1))
	sink.Sentence(ggaSentence(base.Add(200*time.Millisecond), 1, 1))
	assertNoStatus(t, harness.server.published)
}

func TestFlushDeadlinePublishesAfterOnePointFiveSeconds(t *testing.T) {
	harness := startAppHarness(t, slog.LevelInfo)
	defer harness.stop(t)
	sink := harness.sink(t)
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	sink.Sentence(ggaSentence(base, 1, 1))
	select {
	case deadline := <-harness.flushTimer.deadlines:
		if want := base.Add(1500 * time.Millisecond); !deadline.Equal(want) {
			t.Fatalf("flush deadline = %v, want %v", deadline, want)
		}
	case <-time.After(time.Second):
		t.Fatal("first sentence did not schedule a flush deadline")
	}
	harness.flushTimer.ch <- base.Add(1499 * time.Millisecond)
	assertNoStatus(t, harness.server.published)
	harness.flushTimer.ch <- base.Add(1500 * time.Millisecond)
	_ = receiveStatus(t, harness.server.published)
}

func TestResetClearsIncompleteAggregateCycle(t *testing.T) {
	harness := startAppHarness(t, slog.LevelInfo)
	defer harness.stop(t)
	sink := harness.sink(t)
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	withPosition := ggaSentence(base, 1, 1)
	withPosition.GGA.Latitude = nmea.Field[float64]{Value: 31.2, Valid: true}
	withPosition.GGA.Longitude = nmea.Field[float64]{Value: 121.5, Valid: true}
	sink.Sentence(withPosition)
	sink.Reset()
	sink.Sentence(ggaSentence(base.Add(time.Second), 2, 1))
	harness.flushTimer.ch <- base.Add(3 * time.Second)
	status := receiveStatus(t, harness.server.published)
	if status.FieldValidityMask&(model.FullLatitudeValid|model.FullLongitudeValid) != 0 {
		t.Fatalf("pre-reset position leaked: %+v", status)
	}
}

func TestNoInputProducesNoStatusOrHeartbeat(t *testing.T) {
	harness := startAppHarness(t, slog.LevelInfo)
	defer harness.stop(t)
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 10; i++ {
		harness.ticker.ch <- base.Add(time.Duration(i) * time.Second)
	}
	assertNoStatus(t, harness.server.published)
}

func TestNoFixInputPublishesExplicitInvalidValue(t *testing.T) {
	harness := startAppHarness(t, slog.LevelInfo)
	defer harness.stop(t)
	sink := harness.sink(t)
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	sink.Sentence(ggaSentence(base, 1, 0))
	sink.Sentence(ggaSentence(base.Add(time.Second), 2, 0))
	status := receiveStatus(t, harness.server.published)
	if status.FieldValidityMask&model.FullValidValid == 0 || status.Valid != 0 {
		t.Fatalf("no-fix status did not report valid=0: %+v", status)
	}
}

func TestPublishedCyclesRecordGSVCompleteness(t *testing.T) {
	harness := startAppHarness(t, slog.LevelInfo)
	defer harness.stop(t)
	sink := harness.sink(t)
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	sink.Sentence(ggaSentence(base, 1, 1))
	sink.Sentence(gsvSentence(base.Add(100*time.Millisecond), 1, 1, 0))
	sink.Sentence(ggaSentence(base.Add(time.Second), 2, 1))
	_ = receiveStatus(t, harness.server.published)
	complete := harness.app.stats.SnapshotReset()
	if complete.GSVComplete != 1 || complete.GSVIncomplete != 0 {
		t.Fatalf("complete GSV counters = %+v", complete)
	}

	sink.Sentence(gsvSentence(base.Add(1100*time.Millisecond), 2, 1, 5))
	sink.Sentence(ggaSentence(base.Add(2*time.Second), 3, 1))
	_ = receiveStatus(t, harness.server.published)
	incomplete := harness.app.stats.SnapshotReset()
	if incomplete.GSVComplete != 0 || incomplete.GSVIncomplete != 1 {
		t.Fatalf("incomplete GSV counters = %+v", incomplete)
	}
}

func TestSlowPublisherDoesNotBlockUDPSink(t *testing.T) {
	gate := make(chan struct{})
	harness := startAppHarnessWithGate(t, slog.LevelInfo, gate)
	sink := harness.sink(t)
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	sink.Sentence(ggaSentence(base, 1, 1))
	sink.Sentence(ggaSentence(base.Add(time.Second), 2, 1))

	done := make(chan struct{})
	go func() {
		sink.Sentence(ggaSentence(base.Add(2*time.Second), 3, 1))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("UDP sink blocked behind slow StatusServer.Publish")
	}
	close(gate)
	harness.stop(t)
}

func TestSlowPublisherDoesNotDropGloballyQueuedStatuses(t *testing.T) {
	gate := make(chan struct{})
	harness := startAppHarnessWithGate(t, slog.LevelInfo, gate)
	sink := harness.sink(t)
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	sink.Sentence(ggaSentence(base, 1, 1))
	sink.Sentence(ggaSentence(base.Add(time.Second), 2, 1))
	select {
	case <-harness.server.publishEntered:
	case <-time.After(time.Second):
		t.Fatal("publisher did not receive the first status")
	}
	sink.Sentence(ggaSentence(base.Add(2*time.Second), 3, 1))
	sink.Sentence(ggaSentence(base.Add(3*time.Second), 4, 1))
	close(gate)

	for index := 0; index < 3; index++ {
		_ = receiveStatus(t, harness.server.published)
	}
	harness.stop(t)
}

func TestShutdownStopsUDPAndTCP(t *testing.T) {
	harness := startAppHarness(t, slog.LevelInfo)
	_ = harness.sink(t)
	harness.cancel()
	select {
	case err := <-harness.done:
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("App.Run did not stop")
	}
	select {
	case <-harness.input.stopped:
	default:
		t.Fatal("UDP input context was not canceled")
	}
	select {
	case <-harness.server.stopped:
	default:
		t.Fatal("TCP server context was not canceled")
	}
}

func TestTCPStartFailureStopsApp(t *testing.T) {
	input := newFakeUDPInput()
	server := newFakeStatusServer()
	server.runErr = errors.New("listen failed")
	app := newApp(input, server, observe.NewStats(), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), newManualTicker(), newManualDeadlineTimer(), time.Now)
	if err := app.Run(context.Background()); err == nil || err.Error() != "listen failed" {
		t.Fatalf("Run error = %v", err)
	}
}

func TestUnexpectedUDPInputExitStopsApp(t *testing.T) {
	input := newFakeUDPInput()
	want := errors.New("input failed")
	input.runErr = want
	server := newFakeStatusServer()
	app := newApp(input, server, observe.NewStats(), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), newManualTicker(), newManualDeadlineTimer(), time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("Run error = %v, want %v", err, want)
		}
	case <-time.After(100 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("App continued running after UDP input exited")
	}
}

func TestInfoSummaryIsSixtySecondsAndDebugRetainsOneSecondSnapshots(t *testing.T) {
	info := startAppHarness(t, slog.LevelInfo)
	sink := info.sink(t)
	info.app.stats.RecordGSVComplete()
	info.app.stats.RecordGSVIncomplete()
	info.app.stats.RecordTCPConnection()
	info.app.stats.RecordTCPDisconnection()
	info.app.stats.RecordTCPRejection()
	info.app.stats.RecordTCPSubscription(protocol.StatusSimple)
	info.app.stats.RecordTCPSubscription(protocol.StatusFull)
	info.app.stats.RecordSlowClientReplacement()
	for i := 0; i < 59; i++ {
		sink.DatagramReceived(10)
		info.ticker.ch <- info.startedAt.Add(time.Duration(i+1) * time.Second)
	}
	waitFor(t, func() bool { return info.app.summaryIntervals() == 59 })
	if strings.Contains(info.logs.String(), "one-second input summary") || strings.Contains(info.logs.String(), "60-second input summary") {
		t.Fatalf("info log emitted summary too early: %s", info.logs.String())
	}
	info.ticker.ch <- info.startedAt.Add(60 * time.Second)
	waitFor(t, func() bool { return strings.Count(info.logs.String(), "60-second input summary") == 1 })
	if strings.Contains(info.logs.String(), "one-second input summary") {
		t.Fatalf("info log contains debug snapshots: %s", info.logs.String())
	}
	for _, field := range []string{
		"gsv_complete=1", "gsv_incomplete=1", "tcp_connections=1", "tcp_disconnections=1",
		"tcp_rejections=1", "tcp_simple_subscriptions=1", "tcp_full_subscriptions=1",
		"slow_client_replacements=1",
	} {
		if !strings.Contains(info.logs.String(), field) {
			t.Fatalf("info summary is missing %s: %s", field, info.logs.String())
		}
	}
	info.stop(t)

	debug := startAppHarness(t, slog.LevelDebug)
	debugSink := debug.sink(t)
	debugSink.DatagramReceived(10)
	debug.ticker.ch <- debug.startedAt.Add(time.Second)
	waitFor(t, func() bool { return strings.Contains(debug.logs.String(), "one-second input summary") })
	debug.stop(t)
}

func TestInputInterruptionAndRecoveryAreSingleTransitions(t *testing.T) {
	harness := startAppHarness(t, slog.LevelInfo)
	sink := harness.sink(t)
	harness.ticker.ch <- harness.startedAt.Add(5 * time.Second)
	harness.ticker.ch <- harness.startedAt.Add(6 * time.Second)
	waitFor(t, func() bool { return strings.Count(harness.logs.String(), "GNSS input interrupted") == 1 })
	sink.Sentence(ggaSentence(harness.startedAt.Add(7*time.Second), 7, 1))
	waitFor(t, func() bool { return strings.Count(harness.logs.String(), "GNSS input recovered") == 1 })
	harness.stop(t)
}

func TestRepeatedWarningsAreRateLimitedWithSuppressedCount(t *testing.T) {
	input := newFakeUDPInput()
	server := newFakeStatusServer()
	ticker := newManualTicker()
	logs := &bytes.Buffer{}
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	app := newApp(input, server, observe.NewStats(), slog.New(slog.NewTextHandler(logs, nil)), ticker, newManualDeadlineTimer(), func() time.Time { return now })
	sink := &inputSink{app: app}
	sink.DatagramRejected(udpinput.RejectNUL)
	sink.DatagramRejected(udpinput.RejectNUL)
	sink.DatagramRejected(udpinput.RejectNUL)
	if got := strings.Count(logs.String(), "UDP datagram rejected"); got != 1 {
		t.Fatalf("warning count = %d, want 1: %s", got, logs.String())
	}
	now = now.Add(61 * time.Second)
	sink.DatagramRejected(udpinput.RejectNUL)
	if got := strings.Count(logs.String(), "UDP datagram rejected"); got != 2 || !strings.Contains(logs.String(), "suppressed=2") {
		t.Fatalf("rate-limit follow-up missing suppressed count: %s", logs.String())
	}
}

func TestInputLifecycleEventsAreLoggedAndRateLimited(t *testing.T) {
	logs := &bytes.Buffer{}
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	app := newApp(newFakeUDPInput(), newFakeStatusServer(), observe.NewStats(), slog.New(slog.NewTextHandler(logs, nil)), newManualTicker(), newManualDeadlineTimer(), func() time.Time { return now })
	sink := &inputSink{app: app}

	sink.InputEvent(udpinput.InputEvent{Kind: udpinput.InputBindFailed, Err: errors.New("bind"), RetryIn: time.Second})
	sink.InputEvent(udpinput.InputEvent{Kind: udpinput.InputBindFailed, Err: errors.New("bind again"), RetryIn: 2 * time.Second})
	if got := strings.Count(logs.String(), "UDP input bind failed"); got != 1 {
		t.Fatalf("bind warning count = %d: %s", got, logs.String())
	}
	now = now.Add(61 * time.Second)
	sink.InputEvent(udpinput.InputEvent{Kind: udpinput.InputBindFailed, Err: errors.New("bind later"), RetryIn: 4 * time.Second})
	if !strings.Contains(logs.String(), "suppressed=1") {
		t.Fatalf("bind suppression count missing: %s", logs.String())
	}
	sink.InputEvent(udpinput.InputEvent{Kind: udpinput.InputReadFailed, Err: errors.New("read"), RetryIn: time.Second})
	sink.InputEvent(udpinput.InputEvent{
		Kind:      udpinput.InputSocketReady,
		Recovered: true,
		SocketInfo: udpinput.SocketInfo{
			RequestedReadBuffer: 256 * 1024,
			ActualReadBuffer:    128 * 1024,
			KernelReadBuffer:    256 * 1024,
			DropSource:          udpinput.DropUnavailable,
		},
	})
	sink.InputEvent(udpinput.InputEvent{Kind: udpinput.InputSocketClosed})
	for _, message := range []string{
		"UDP input read failed", "UDP input socket ready", "UDP input recovered",
		"UDP drop observation unavailable", "UDP input socket closed",
	} {
		if !strings.Contains(logs.String(), message) {
			t.Fatalf("lifecycle log is missing %q: %s", message, logs.String())
		}
	}
}

type appHarness struct {
	app        *App
	input      *fakeUDPInput
	server     *fakeStatusServer
	ticker     *manualTicker
	flushTimer *manualDeadlineTimer
	logs       *synchronizedBuffer
	startedAt  time.Time
	cancel     context.CancelFunc
	done       chan error
}

func startAppHarness(t *testing.T, level slog.Level) *appHarness {
	t.Helper()
	return startAppHarnessWithGate(t, level, nil)
}

func startAppHarnessWithGate(t *testing.T, level slog.Level, gate <-chan struct{}, clocks ...func() time.Time) *appHarness {
	t.Helper()
	input := newFakeUDPInput()
	server := newFakeStatusServer()
	server.publishGate = gate
	ticker := newManualTicker()
	flushTimer := newManualDeadlineTimer()
	logs := &synchronizedBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: level}))
	startedAt := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return startedAt }
	if len(clocks) != 0 {
		clock = clocks[0]
	}
	app := newApp(input, server, observe.NewStats(), logger, ticker, flushTimer, clock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	return &appHarness{app: app, input: input, server: server, ticker: ticker, flushTimer: flushTimer, logs: logs, startedAt: startedAt, cancel: cancel, done: done}
}

func (h *appHarness) sink(t *testing.T) udpinput.Sink {
	t.Helper()
	select {
	case sink := <-h.input.started:
		return sink
	case <-time.After(time.Second):
		t.Fatal("UDP input did not receive a sink")
		return nil
	}
}

func (h *appHarness) stop(t *testing.T) {
	t.Helper()
	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("App.Run error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("App.Run did not stop")
	}
}

func ggaSentence(receivedAt time.Time, second int64, quality uint8) nmea.Sentence {
	return nmea.Sentence{
		Talker:     "GN",
		Kind:       nmea.KindGGA,
		ReceivedAt: receivedAt,
		GGA: &nmea.GGA{
			MillisOfDay: second * 1000,
			TimeValid:   true,
			Quality:     nmea.Field[uint8]{Value: quality, Valid: true},
		},
	}
}

func gsvSentence(receivedAt time.Time, total, number int, visible uint8) nmea.Sentence {
	return nmea.Sentence{
		Talker:     "GP",
		Kind:       nmea.KindGSV,
		ReceivedAt: receivedAt,
		GSV: &nmea.GSV{
			TotalMessages: total,
			MessageNumber: number,
			VisibleCount:  nmea.Field[uint8]{Value: visible, Valid: true},
		},
	}
}

func receiveStatus(t *testing.T, statuses <-chan model.FullStatus) model.FullStatus {
	t.Helper()
	select {
	case status := <-statuses:
		return status
	case <-time.After(time.Second):
		t.Fatal("status was not published")
		return model.FullStatus{}
	}
}

func assertNoStatus(t *testing.T, statuses <-chan model.FullStatus) {
	t.Helper()
	select {
	case status := <-statuses:
		t.Fatalf("unexpected status: %+v", status)
	case <-time.After(30 * time.Millisecond):
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met")
}
