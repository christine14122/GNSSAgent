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

type warningState struct {
	last       time.Time
	suppressed uint64
}

type App struct {
	input  UDPInput
	server StatusServer
	stats  *observe.Stats
	logger *slog.Logger
	ticker intervalTicker
	clock  func() time.Time

	aggregateMu sync.Mutex
	aggregator  *aggregate.Aggregator
	publish     chan model.FullStatus

	stateMu         sync.Mutex
	startedAt       time.Time
	interrupted     bool
	warnings        map[string]warningState
	summary         observe.Snapshot
	summaryInterval int
}

func New(input UDPInput, server StatusServer, stats *observe.Stats, logger *slog.Logger) *App {
	return newApp(input, server, stats, logger, realTicker{ticker: time.NewTicker(time.Second)}, time.Now)
}

func newApp(input UDPInput, server StatusServer, stats *observe.Stats, logger *slog.Logger, ticker intervalTicker, clock func() time.Time) *App {
	if stats == nil {
		stats = observe.NewStats()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &App{
		input:      input,
		server:     server,
		stats:      stats,
		logger:     logger,
		ticker:     ticker,
		clock:      clock,
		aggregator: aggregate.New(),
		publish:    make(chan model.FullStatus, 1),
		startedAt:  clock(),
		warnings:   make(map[string]warningState),
	}
}

func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer a.ticker.Stop()

	serverDone := make(chan error, 1)
	inputDone := make(chan error, 1)
	go func() { serverDone <- a.server.Run(ctx) }()
	go func() { inputDone <- a.input.Run(ctx, &inputSink{app: a}) }()
	go a.publishLoop(ctx)

	for {
		select {
		case now := <-a.ticker.C():
			a.onTick(now)
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
			a.logger.Error("UDP input stopped unexpectedly", "error", err)
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
		case status := <-a.publish:
			a.server.Publish(status)
			a.stats.RecordPublishedCycle()
		case <-ctx.Done():
			return
		}
	}
}

func (a *App) offerStatus(status model.FullStatus) {
	select {
	case a.publish <- status:
		return
	default:
	}
	select {
	case <-a.publish:
	default:
	}
	select {
	case a.publish <- status:
	default:
	}
}

func (a *App) onTick(now time.Time) {
	a.aggregateMu.Lock()
	status, ready := a.aggregator.FlushExpired(now)
	a.aggregateMu.Unlock()
	if ready {
		a.offerStatus(status)
	}

	snapshot := a.stats.SnapshotReset()
	if a.logger.Enabled(context.Background(), slog.LevelDebug) {
		a.logger.Debug("one-second input summary",
			"udp_datagrams", snapshot.UDPDatagrams,
			"udp_bytes", snapshot.UDPBytes,
			"udp_rejects", snapshot.UDPRejects,
			"valid_nmea", snapshot.ValidNMEA,
			"kernel_drops", snapshot.KernelDrops,
			"published_cycles", snapshot.PublishedCycles)
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
			"published_cycles", summary.PublishedCycles)
	}
	if becameInterrupted {
		a.logger.Warn("GNSS input interrupted", "silence_seconds", 5)
	}
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
	status, ready := s.app.aggregator.Add(sentence)
	s.app.aggregateMu.Unlock()
	if ready {
		s.app.offerStatus(status)
	}
}

func (s *inputSink) Reset() {
	s.app.aggregateMu.Lock()
	s.app.aggregator.Clear()
	s.app.aggregateMu.Unlock()
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

func (s *inputSink) SocketReady(info udpinput.SocketInfo) {
	s.app.logger.Info("UDP input socket ready",
		"requested_rcvbuf", info.RequestedReadBuffer,
		"actual_rcvbuf", info.ActualReadBuffer,
		"drop_source", info.DropSource)
	if info.ActualReadBuffer > 0 && info.ActualReadBuffer < info.RequestedReadBuffer {
		s.app.warnRateLimited("small-rcvbuf", "UDP receive buffer below requested size",
			"requested", info.RequestedReadBuffer, "actual", info.ActualReadBuffer)
	}
}
