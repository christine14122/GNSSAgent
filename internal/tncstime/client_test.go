package tncstime

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"gnssagent/internal/nmea"
)

func quietClient() *Client { return New(slog.New(slog.NewTextHandler(io.Discard, nil))) }

func utcSentence(ms int64) nmea.Sentence {
	return nmea.Sentence{Kind: nmea.KindRMC, RMC: &nmea.RMC{
		Date:        nmea.Field[time.Time]{Value: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), Valid: true},
		MillisOfDay: ms, TimeValid: true,
		Status: nmea.Field[byte]{Value: 'V', Valid: true}, // Navigation validity must not gate time.
	}}
}

func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}

func idle(c *Client) bool { c.mu.Lock(); defer c.mu.Unlock(); return c.connected && !c.busy }

func readFrame(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n > maxFrameSize {
		return nil, errors.New("oversized frame")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

func readRequest(r io.Reader) (request, error) {
	data, err := readFrame(r)
	if err != nil {
		return request{}, err
	}
	var req request
	var kind uint64
	for len(data) > 0 {
		f, rest, err := consume(data)
		if err != nil {
			return req, err
		}
		data = rest
		if f.number == 1 {
			kind = f.integer
		}
		if f.number != 2 {
			continue
		}
		payload := f.bytes
		for len(payload) > 0 {
			v, rest, err := consume(payload)
			if err != nil {
				return req, err
			}
			payload = rest
			if v.number == 1 {
				req.timestamp = int64(v.integer)
			}
			if v.number == 2 {
				req.trusted = v.integer != 0
			}
		}
	}
	if kind != setTimeRequest {
		return req, errors.New("wrong request type")
	}
	return req, nil
}

var successFrame = []byte{0, 0, 0, 8, 0x08, 0xb0, 0xa2, 0x0c, 0x12, 0x02, 0x08, 0x01}

func TestFreshTimeIntervalSilenceAndRecovery(t *testing.T) {
	c := quietClient()
	base := time.Now()
	var elapsed atomic.Int64
	c.now = func() time.Time { return base.Add(time.Duration(elapsed.Load())) }
	client, server := net.Pipe()
	defer server.Close()
	c.dial = func(context.Context) (net.Conn, error) { return client, nil }
	received := make(chan request, 10)
	go func() {
		for {
			r, err := readRequest(server)
			if err != nil {
				return
			}
			received <- r
			if _, err := server.Write(successFrame); err != nil {
				return
			}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	defer func() { cancel(); <-done }()
	eventually(t, func() bool { return idle(c) })
	get := func() request {
		t.Helper()
		select {
		case r := <-received:
			return r
		case <-time.After(3 * time.Second):
			t.Fatal("missing request")
			return request{}
		}
	}
	assertQuiet := func() {
		t.Helper()
		select {
		case r := <-received:
			t.Fatalf("unexpected request: %+v", r)
		case <-time.After(30 * time.Millisecond):
		}
	}
	c.Sentence(utcSentence(123))
	first := get()
	want, _ := sentenceUTC(utcSentence(123))
	if first != (request{timestamp: want, trusted: true}) {
		t.Fatalf("first=%+v", first)
	}
	eventually(t, func() bool { return idle(c) })
	elapsed.Store(int64(29 * time.Second))
	c.Sentence(utcSentence(29_000))
	assertQuiet()
	elapsed.Store(int64(30 * time.Second))
	assertQuiet() // Timer expiry alone must not send the cached 29-second UTC.
	c.Sentence(nmea.Sentence{Kind: nmea.KindGGA})
	assertQuiet()
	c.Sentence(utcSentence(30_456))
	second := get()
	want, _ = sentenceUTC(utcSentence(30_456))
	if second.timestamp != want || !second.trusted {
		t.Fatalf("second=%+v", second)
	}
	eventually(t, func() bool { return idle(c) })
	// Non-UTC GNSS data refreshes the outage clock.
	elapsed.Store(int64(29 * time.Minute))
	c.Sentence(nmea.Sentence{Kind: nmea.KindGSV})
	elapsed.Store(int64(31 * time.Minute))
	assertQuiet()
	elapsed.Store(int64(59 * time.Minute))
	if lost := get(); lost != (request{}) {
		t.Fatalf("outage must be zero/false: %+v", lost)
	}
	eventually(t, func() bool { return idle(c) })
	elapsed.Store(int64(60 * time.Minute))
	// A complete additional tick verifies that successful invalidation is not repeated.
	select {
	case r := <-received:
		t.Fatalf("duplicate invalidation: %+v", r)
	case <-time.After(1100 * time.Millisecond):
	}
	c.Sentence(utcSentence(3_600_123))
	if recovery := get(); !recovery.trusted || recovery.timestamp == 0 {
		t.Fatalf("recovery=%+v", recovery)
	}
}

func TestStartupRetriesUntilCancelled(t *testing.T) {
	c := quietClient()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts, waits := 0, 0
	c.dial = func(context.Context) (net.Conn, error) { attempts++; return nil, errors.New("offline") }
	c.wait = func(_ context.Context, d time.Duration) bool {
		if d != 3*time.Second {
			t.Fatalf("retry interval=%v", d)
		}
		waits++
		if waits == 10 {
			cancel()
		}
		return ctx.Err() == nil
	}
	c.Run(ctx)
	if attempts != 10 || waits != 10 {
		t.Fatalf("attempts=%d waits=%d", attempts, waits)
	}
	c.Sentence(utcSentence(1))
	if len(c.requests) != 0 {
		t.Fatal("disconnected client queued time")
	}
}

func TestStartupRetryCanRecoverAndCancel(t *testing.T) {
	c := quietClient()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	attempts := 0
	c.dial = func(context.Context) (net.Conn, error) {
		attempts++
		if attempts < 8 {
			return nil, errors.New("offline")
		}
		return client, nil
	}
	c.wait = func(context.Context, time.Duration) bool { return true }
	if got := c.connect(context.Background()); got != client || attempts != 8 {
		t.Fatal("client did not recover after more than four attempts")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if wait(ctx, time.Hour) {
		t.Fatal("cancelled wait succeeded")
	}
	if c.connect(ctx) != nil {
		t.Fatal("dial after cancellation")
	}
}

func TestUTCDoesNotDependOnQualityAndRequiresCompleteDate(t *testing.T) {
	s := utcSentence(123)
	if utc, ok := sentenceUTC(s); !ok || utc != 1792627200123 {
		t.Fatalf("UTC=%d ok=%v", utc, ok)
	}
	zda := nmea.Sentence{Kind: nmea.KindZDA, ZDA: &nmea.ZDA{Date: s.RMC.Date, TimeValid: true, MillisOfDay: 456}}
	if utc, ok := sentenceUTC(zda); !ok || utc != 1792627200456 {
		t.Fatalf("ZDA=%d ok=%v", utc, ok)
	}
	s.RMC.Date.Valid = false
	if _, ok := sentenceUTC(s); ok {
		t.Fatal("missing date accepted")
	}
	s.RMC.Date.Valid = true
	s.RMC.MillisOfDay = 86_400_000
	if _, ok := sentenceUTC(s); ok {
		t.Fatal("invalid time accepted")
	}
	if _, ok := sentenceUTC(nmea.Sentence{Kind: nmea.KindGLL}); ok {
		t.Fatal("undated GLL accepted")
	}
}

func TestNoQueueWhileDisconnectedOrBusy(t *testing.T) {
	c := quietClient()
	c.Sentence(utcSentence(1))
	if len(c.requests) != 0 {
		t.Fatal("queued while disconnected")
	}
	c.connected = true
	c.Sentence(utcSentence(2))
	for i := 0; i < 100; i++ {
		c.Sentence(utcSentence(3))
	}
	if len(c.requests) != 1 {
		t.Fatal("multiple pending UTC requests")
	}
	if r := <-c.requests; r.timestamp%1000 != 2 {
		t.Fatal("first eligible UTC replaced")
	}
}

func TestReconnectWaitsForFreshUTCAndPreservesInterval(t *testing.T) {
	c := quietClient()
	base := time.Now()
	var elapsed atomic.Int64
	c.now = func() time.Time { return base.Add(time.Duration(elapsed.Load())) }
	firstClient, firstServer := net.Pipe()
	secondClient, secondServer := net.Pipe()
	defer firstServer.Close()
	defer secondServer.Close()
	var attempts atomic.Int32
	c.dial = func(context.Context) (net.Conn, error) {
		if attempts.Add(1) == 1 {
			return firstClient, nil
		}
		return secondClient, nil
	}
	c.wait = func(context.Context, time.Duration) bool { return true }
	go func() { readRequest(firstServer); firstServer.Close() }() // Request written; ACK lost.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	defer func() { cancel(); <-done }()
	eventually(t, func() bool { return idle(c) })
	c.Sentence(utcSentence(1))
	eventually(t, func() bool { return attempts.Load() == 2 && idle(c) })
	received := make(chan request, 1)
	go func() {
		r, err := readRequest(secondServer)
		if err == nil {
			received <- r
			secondServer.Write(successFrame)
		}
	}()
	elapsed.Store(int64(29 * time.Second))
	c.Sentence(utcSentence(29_000))
	select {
	case r := <-received:
		t.Fatalf("resent before interval: %+v", r)
	case <-time.After(30 * time.Millisecond):
	}
	elapsed.Store(int64(30 * time.Second))
	select {
	case r := <-received:
		t.Fatalf("cached time on reconnect: %+v", r)
	case <-time.After(30 * time.Millisecond):
	}
	c.Sentence(utcSentence(30_123))
	select {
	case r := <-received:
		want, _ := sentenceUTC(utcSentence(30_123))
		if r.timestamp != want {
			t.Fatalf("replayed old UTC: %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("fresh time not sent after reconnect")
	}
}

func TestNoDataSinceStartupInvalidatesAndRetriesRejection(t *testing.T) {
	c := quietClient()
	base := time.Now()
	var elapsed atomic.Int64
	c.now = func() time.Time { return base.Add(time.Duration(elapsed.Load())) }
	client, server := net.Pipe()
	defer server.Close()
	c.dial = func(context.Context) (net.Conn, error) { return client, nil }
	received := make(chan request, 2)
	go func() {
		for i := 0; i < 2; i++ {
			r, err := readRequest(server)
			if err != nil {
				return
			}
			received <- r
			frame := successFrame
			if i == 0 {
				frame = []byte{0, 0, 0, 4, 8, 0xb0, 0xa2, 0x0c}
			} // success omitted = false.
			if _, err := server.Write(frame); err != nil {
				return
			}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	defer func() { cancel(); <-done }()
	eventually(t, func() bool { return idle(c) })
	elapsed.Store(int64(silenceTimeout))
	for i := 0; i < 2; i++ {
		select {
		case r := <-received:
			if r != (request{}) {
				t.Fatalf("invalidation=%+v", r)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing invalidation")
		}
		eventually(t, func() bool { return idle(c) })
		if i == 0 {
			c.mu.Lock()
			marked := c.invalidated
			c.mu.Unlock()
			if marked {
				t.Fatal("rejection marked as delivered")
			}
			elapsed.Store(int64(silenceTimeout + retryInterval))
		}
	}
	c.mu.Lock()
	marked := c.invalidated
	c.mu.Unlock()
	if !marked {
		t.Fatal("successful retry not recorded")
	}
}
