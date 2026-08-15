package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"gnssagent/internal/aggregate"
	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
	"gnssagent/internal/observe"
	"gnssagent/internal/udpinput"
)

const aggregateFlushDelay = 1500 * time.Millisecond

type UDPInput interface {
	Run(context.Context, udpinput.Sink) error
}

type StatusServer interface {
	Run(context.Context) error
	Publish(model.FullStatus)
}

type intervalTicker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct {
	ticker *time.Ticker
}

func (t realTicker) C() <-chan time.Time { return t.ticker.C }
func (t realTicker) Stop()               { t.ticker.Stop() }

type deadlineTimer interface {
	C() <-chan time.Time
	Reset(time.Time)
	Stop()
}

type realDeadlineTimer struct {
	timer *time.Timer
}

func newRealDeadlineTimer() *realDeadlineTimer {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	return &realDeadlineTimer{timer: timer}
}

func (t *realDeadlineTimer) C() <-chan time.Time { return t.timer.C }
func (t *realDeadlineTimer) Reset(deadline time.Time) {
	delay := time.Until(deadline)
	if delay < 0 {
		delay = 0
	}
	t.timer.Reset(delay)
}
func (t *realDeadlineTimer) Stop() { t.timer.Stop() }

type warningState struct {
	last       time.Time
	suppressed uint64
}

type App struct {
	input      UDPInput
	server     StatusServer
	stats      *observe.Stats
	logger     *slog.Logger
	ticker     intervalTicker
	flushTimer deadlineTimer
	clock      func() time.Time

	aggregateMu   sync.Mutex
	aggregator    *aggregate.Aggregator
	flushDeadline time.Time
	currentHasGSV bool

	publishMu    sync.Mutex
	publishQueue []model.FullStatus
	publishWake  chan struct{}

	stateMu         sync.Mutex
	startedAt       time.Time
	interrupted     bool
	warnings        map[string]warningState
	summary         observe.Snapshot
	summaryInterval int
}

func New(input UDPInput, server StatusServer, stats *observe.Stats, logger *slog.Logger) *App {
	return newApp(input, server, stats, logger, realTicker{ticker: time.NewTicker(time.Second)}, newRealDeadlineTimer(), time.Now)
}

func newApp(input UDPInput, server StatusServer, stats *observe.Stats, logger *slog.Logger, ticker intervalTicker, flushTimer deadlineTimer, clock func() time.Time) *App {
	if stats == nil {
		stats = observe.NewStats()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &App{
		input:       input,
		server:      server,
		stats:       stats,
		logger:      logger,
		ticker:      ticker,
		flushTimer:  flushTimer,
		clock:       clock,
		aggregator:  aggregate.New(),
		publishWake: make(chan struct{}, 1),
		startedAt:   clock(),
		warnings:    make(map[string]warningState),
	}
}

func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer a.ticker.Stop()
	defer a.flushTimer.Stop()

	serverDone := make(chan error, 1)
	inputDone := make(chan error, 1)
	go func() { serverDone <- a.server.Run(ctx) }()
	go func() { inputDone <- a.input.Run(ctx, &inputSink{app: a}) }()
	go a.publishLoop(ctx)

	for {
		select {
		case now := <-a.ticker.C():
			a.onTick(now)
		case now := <-a.flushTimer.C():
			a.onFlushDeadline(now)
		case err := <-serverDone:
			serverDone = nil
			if ctx.Err() != nil {
				a.waitForComponents(nil, inputDone)
				return nil
			}
			cancel()
			a.waitForComponents(nil, inputDone)
			if err == nil {
				return errors.New("TCP status server stopped unexpectedly")
			}
			return err
		case err := <-inputDone:
			inputDone = nil
			if ctx.Err() != nil {
				a.waitForComponents(serverDone, nil)
				return nil
			}
			cancel()
			a.waitForComponents(serverDone, nil)
			if err == nil {
				return errors.New("UDP input stopped unexpectedly")
			}
			return errors.Join(errors.New("UDP input stopped unexpectedly"), err)
		case <-ctx.Done():
			cancel()
			a.waitForComponents(serverDone, inputDone)
			return nil
		}
	}
}

func (a *App) waitForComponents(serverDone, inputDone <-chan error) {
	for serverDone != nil || inputDone != nil {
		select {
		case <-serverDone:
			serverDone = nil
		case <-inputDone:
			inputDone = nil
		}
	}
}

func (a *App) publishLoop(ctx context.Context) {
	for {
		select {
		case <-a.publishWake:
			for {
				status, ok := a.nextStatus()
				if !ok {
					break
				}
				a.server.Publish(status)
				a.stats.RecordPublishedCycle()
			}
		case <-ctx.Done():
			return
		}
	}
}

func (a *App) offerStatus(status model.FullStatus) {
	a.publishMu.Lock()
	a.publishQueue = append(a.publishQueue, status)
	a.publishMu.Unlock()
	select {
	case a.publishWake <- struct{}{}:
	default:
	}
}

func (a *App) nextStatus() (model.FullStatus, bool) {
	a.publishMu.Lock()
	defer a.publishMu.Unlock()
	if len(a.publishQueue) == 0 {
		return model.FullStatus{}, false
	}
	status := a.publishQueue[0]
	if len(a.publishQueue) == 1 {
		a.publishQueue = nil
	} else {
		a.publishQueue[0] = model.FullStatus{}
		a.publishQueue = a.publishQueue[1:]
	}
	return status, true
}

func (a *App) onTick(now time.Time) {
	snapshot := a.stats.SnapshotReset()
	if a.logger.Enabled(context.Background(), slog.LevelDebug) {
		a.logger.Debug("one-second input summary",
			"udp_datagrams", snapshot.UDPDatagrams,
			"udp_bytes", snapshot.UDPBytes,
			"udp_rejects", snapshot.UDPRejects,
			"valid_nmea", snapshot.ValidNMEA,
			"kernel_drops", snapshot.KernelDrops,
			"gsv_complete", snapshot.GSVComplete,
			"gsv_incomplete", snapshot.GSVIncomplete,
			"published_cycles", snapshot.PublishedCycles,
			"tcp_connections", snapshot.TCPConnections,
			"tcp_disconnections", snapshot.TCPDisconnections,
			"tcp_rejections", snapshot.TCPRejections,
			"tcp_simple_subscriptions", snapshot.TCPSimpleSubscriptions,
			"tcp_full_subscriptions", snapshot.TCPFullSubscriptions,
			"slow_client_replacements", snapshot.SlowClientReplacements)
	}

	a.stateMu.Lock()
	a.summary.Add(snapshot)
	a.summaryInterval++
	intervals := a.summaryInterval
	var summary observe.Snapshot
	if a.summaryInterval == 60 {
		summary = a.summary
		a.summary = observe.Snapshot{}
		a.summaryInterval = 0
	}
	reference := snapshot.LastValidNMEA
	if reference.IsZero() {
		reference = a.startedAt
	}
	becameInterrupted := !a.interrupted && now.Sub(reference) >= 5*time.Second
	if becameInterrupted {
		a.interrupted = true
	}
	a.stateMu.Unlock()

	if intervals == 60 {
		a.logger.Info("60-second input summary",
			"window_seconds", 60,
			"udp_datagrams", summary.UDPDatagrams,
			"udp_bytes", summary.UDPBytes,
			"udp_rejects", summary.UDPRejects,
			"checksum_failures", summary.ChecksumFailures,
			"parser_failures", summary.ParserFailures,
			"valid_nmea", summary.ValidNMEA,
			"kernel_drops", summary.KernelDrops,
			"kernel_drops_by_source", summary.KernelDropsBySource,
			"gsv_complete", summary.GSVComplete,
			"gsv_incomplete", summary.GSVIncomplete,
			"published_cycles", summary.PublishedCycles,
			"tcp_connections", summary.TCPConnections,
			"tcp_disconnections", summary.TCPDisconnections,
			"tcp_rejections", summary.TCPRejections,
			"tcp_subscriptions", summary.TCPSubscriptions,
			"tcp_simple_subscriptions", summary.TCPSimpleSubscriptions,
			"tcp_full_subscriptions", summary.TCPFullSubscriptions,
			"slow_client_replacements", summary.SlowClientReplacements)
	}
	if becameInterrupted {
		a.logger.Warn("GNSS input interrupted", "silence_seconds", 5)
	}
}

func (a *App) onFlushDeadline(now time.Time) {
	a.aggregateMu.Lock()
	if a.flushDeadline.IsZero() || now.Before(a.flushDeadline) {
		a.aggregateMu.Unlock()
		return
	}
	status, ready := a.aggregator.FlushExpired(now)
	hadGSV := a.currentHasGSV
	a.flushDeadline = time.Time{}
	a.currentHasGSV = false
	if ready {
		a.recordGSVCompleteness(status, hadGSV)
		a.offerStatus(status)
	}
	a.aggregateMu.Unlock()
}

func (a *App) summaryIntervals() int {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.summaryInterval
}

func (a *App) recoverInput(receivedAt time.Time) {
	a.stateMu.Lock()
	wasInterrupted := a.interrupted
	if wasInterrupted {
		a.interrupted = false
	}
	a.stateMu.Unlock()
	if wasInterrupted {
		a.logger.Info("GNSS input recovered", "received_at", receivedAt.UTC().Format(time.RFC3339Nano))
	}
}

func (a *App) warnRateLimited(key, message string, attributes ...any) {
	now := a.clock()
	a.stateMu.Lock()
	state := a.warnings[key]
	if !state.last.IsZero() && now.Sub(state.last) < time.Minute {
		state.suppressed++
		a.warnings[key] = state
		a.stateMu.Unlock()
		return
	}
	suppressed := state.suppressed
	a.warnings[key] = warningState{last: now}
	a.stateMu.Unlock()
	attributes = append(attributes, "suppressed", suppressed)
	a.logger.Warn(message, attributes...)
}

type inputSink struct {
	app *App
}

func (s *inputSink) DatagramReceived(bytes int) {
	s.app.stats.RecordDatagram(bytes)
}

func (s *inputSink) Sentence(sentence nmea.Sentence) {
	s.app.stats.RecordValidNMEA(sentence.Talker, sentence.Kind, sentence.ReceivedAt)
	s.app.recoverInput(sentence.ReceivedAt)
	s.app.aggregateMu.Lock()
	hadGSV := s.app.currentHasGSV
	status, ready := s.app.aggregator.Add(sentence)
	if ready {
		s.app.recordGSVCompleteness(status, hadGSV)
		s.app.offerStatus(status)
		s.app.currentHasGSV = sentence.Kind == nmea.KindGSV
		s.app.scheduleFlushLocked(sentence.ReceivedAt)
	} else {
		if sentence.Kind == nmea.KindGSV {
			s.app.currentHasGSV = true
		}
		if s.app.flushDeadline.IsZero() {
			s.app.scheduleFlushLocked(sentence.ReceivedAt)
		}
	}
	s.app.aggregateMu.Unlock()
}

func (s *inputSink) Reset() {
	s.app.aggregateMu.Lock()
	s.app.aggregator.Clear()
	s.app.flushDeadline = time.Time{}
	s.app.currentHasGSV = false
	s.app.flushTimer.Stop()
	s.app.aggregateMu.Unlock()
}

func (a *App) scheduleFlushLocked(firstReceivedAt time.Time) {
	if firstReceivedAt.IsZero() {
		firstReceivedAt = a.clock()
	}
	a.flushDeadline = firstReceivedAt.Add(aggregateFlushDelay)
	a.flushTimer.Reset(a.flushDeadline)
}

func (a *App) recordGSVCompleteness(status model.FullStatus, hadGSV bool) {
	if !hadGSV {
		return
	}
	const completeMask = model.FullGPSSatellitesValid |
		model.FullBeiDouSatellitesValid |
		model.FullGLONASSSatellitesValid |
		model.FullGalileoSatellitesValid
	if status.FieldValidityMask&completeMask != 0 {
		a.stats.RecordGSVComplete()
	} else {
		a.stats.RecordGSVIncomplete()
	}
}

func (s *inputSink) DatagramRejected(reason udpinput.RejectReason) {
	s.app.stats.RecordUDPReject(reason)
	s.app.warnRateLimited("udp-reject", "UDP datagram rejected", "reason", reason)
}

func (s *inputSink) NMEARejected(checksum bool) {
	s.app.stats.RecordNMEAReject(checksum)
	if checksum {
		s.app.warnRateLimited("checksum", "NMEA checksum validation failed")
	} else {
		s.app.warnRateLimited("parser", "NMEA sentence unsupported or invalid")
	}
}

func (s *inputSink) KernelDrops(delta uint64, source udpinput.DropSource) {
	s.app.stats.RecordKernelDrops(delta, source)
	s.app.warnRateLimited("kernel-drops", "UDP receive queue dropped datagrams", "delta", delta, "source", source)
}

func (s *inputSink) InputEvent(event udpinput.InputEvent) {
	switch event.Kind {
	case udpinput.InputBindFailed:
		s.app.warnRateLimited("udp-bind", "UDP input bind failed",
			"error", event.Err, "attempt", event.Attempt, "retry_in", event.RetryIn)
	case udpinput.InputReadFailed:
		s.app.warnRateLimited("udp-read", "UDP input read failed",
			"error", event.Err, "attempt", event.Attempt, "retry_in", event.RetryIn)
	case udpinput.InputSocketReady:
		info := event.SocketInfo
		s.app.logger.Info("UDP input socket ready",
			"requested_rcvbuf", info.RequestedReadBuffer,
			"actual_rcvbuf", info.ActualReadBuffer,
			"kernel_rcvbuf", info.KernelReadBuffer,
			"drop_source", info.DropSource,
			"recovered", event.Recovered)
		if event.Recovered {
			s.app.logger.Info("UDP input recovered")
		}
		if info.DropSource == udpinput.DropUnavailable {
			s.app.warnRateLimited("drop-unavailable", "UDP drop observation unavailable")
		}
		if info.ActualReadBuffer > 0 && info.ActualReadBuffer < info.RequestedReadBuffer {
			s.app.warnRateLimited("small-rcvbuf", "UDP receive buffer below requested size",
				"requested", info.RequestedReadBuffer, "actual", info.ActualReadBuffer,
				"kernel", info.KernelReadBuffer)
		}
	case udpinput.InputSocketClosed:
		s.app.logger.Info("UDP input socket closed")
	}
}
