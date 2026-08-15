package udpinput

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"time"

	"gnssagent/internal/nmea"
)

var (
	loopbackSource = netip.MustParseAddrPort("127.0.0.1:31000")
	remoteSource   = netip.MustParseAddrPort("192.168.7.2:31000")
	errRead        = errors.New("read failed")
)

type listenOutcome struct {
	socket packetSocket
	err    error
}

type fakeFactory struct {
	mu       sync.Mutex
	outcomes []listenOutcome
	calls    int
}

func (f *fakeFactory) Listen(string) (packetSocket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.outcomes) == 0 {
		return nil, errors.New("unexpected Listen call")
	}
	outcome := f.outcomes[0]
	f.outcomes = f.outcomes[1:]
	return outcome.socket, outcome.err
}

type fakeRead struct {
	data   []byte
	result ReadResult
	err    error
}

type procSample struct {
	drops uint64
	ok    bool
}

type fakeSocket struct {
	mu          sync.Mutex
	reads       []fakeRead
	procSamples []procSample
	procIndex   int
	info        SocketInfo
	closed      chan struct{}
	closeOnce   sync.Once
	closeCount  int
}

func newFakeSocket(reads ...fakeRead) *fakeSocket {
	return &fakeSocket{reads: reads, closed: make(chan struct{})}
}

func (s *fakeSocket) Read(buffer []byte) (ReadResult, error) {
	s.mu.Lock()
	if len(s.reads) > 0 {
		read := s.reads[0]
		s.reads = s.reads[1:]
		s.mu.Unlock()
		copy(buffer, read.data)
		if read.result.N == 0 && len(read.data) != 0 {
			read.result.N = len(read.data)
		}
		return read.result, read.err
	}
	s.mu.Unlock()
	<-s.closed
	return ReadResult{}, errors.New("socket closed")
}

func (s *fakeSocket) ProcDrops() (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.procSamples) == 0 {
		return 0, false
	}
	index := s.procIndex
	if index >= len(s.procSamples) {
		index = len(s.procSamples) - 1
	} else {
		s.procIndex++
	}
	return s.procSamples[index].drops, s.procSamples[index].ok
}

func (s *fakeSocket) Info() SocketInfo {
	return s.info
}

func (s *fakeSocket) Close() error {
	s.mu.Lock()
	s.closeCount++
	s.mu.Unlock()
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

type dropEvent struct {
	delta  uint64
	source DropSource
}

type recordingSink struct {
	mu              sync.Mutex
	sentences       []nmea.Sentence
	resets          int
	datagramRejects map[RejectReason]int
	checksumRejects int
	parseRejects    int
	drops           []dropEvent
	socketInfos     []SocketInfo
	datagrams       int
	bytes           int
	ready           chan struct{}
	readyOnce       sync.Once
}

func (s *recordingSink) DatagramReceived(bytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.datagrams++
	s.bytes += bytes
}

func newRecordingSink() *recordingSink {
	return &recordingSink{
		datagramRejects: make(map[RejectReason]int),
		ready:           make(chan struct{}),
	}
}

func (s *recordingSink) Sentence(sentence nmea.Sentence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sentences = append(s.sentences, sentence)
}

func (s *recordingSink) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resets++
}

func (s *recordingSink) DatagramRejected(reason RejectReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.datagramRejects[reason]++
}

func (s *recordingSink) NMEARejected(checksum bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if checksum {
		s.checksumRejects++
	} else {
		s.parseRejects++
	}
}

func (s *recordingSink) KernelDrops(delta uint64, source DropSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drops = append(s.drops, dropEvent{delta: delta, source: source})
}

func (s *recordingSink) SocketReady(info SocketInfo) {
	s.mu.Lock()
	s.socketInfos = append(s.socketInfos, info)
	s.mu.Unlock()
	s.readyOnce.Do(func() { close(s.ready) })
}

func TestManagerRetriesBindWithFixedBackoff(t *testing.T) {
	factory := &fakeFactory{}
	for i := 0; i < 7; i++ {
		factory.outcomes = append(factory.outcomes, listenOutcome{err: errors.New("bind failed")})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sleeps []time.Duration
	manager := newManager("127.0.0.1:29501", factory, func(_ context.Context, duration time.Duration) bool {
		sleeps = append(sleeps, duration)
		if len(sleeps) == 7 {
			cancel()
			return false
		}
		return true
	})
	manager.Run(ctx, newRecordingSink())

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	if !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
}

func TestManagerSuccessfulBindResetsBackoffAndReadErrorRebinds(t *testing.T) {
	socket := newFakeSocket(fakeRead{err: errRead})
	factory := &fakeFactory{outcomes: []listenOutcome{
		{err: errors.New("first bind failed")},
		{socket: socket},
		{err: errors.New("second bind failed")},
	}}
	sink := newRecordingSink()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sleeps []time.Duration
	manager := newManager("127.0.0.1:29501", factory, func(_ context.Context, duration time.Duration) bool {
		sleeps = append(sleeps, duration)
		if len(sleeps) == 3 {
			cancel()
			return false
		}
		return true
	})
	manager.Run(ctx, sink)

	if want := []time.Duration{time.Second, time.Second, 2 * time.Second}; !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	if sink.resets != 1 {
		t.Fatalf("Reset calls = %d, want 1", sink.resets)
	}
	if socket.closeCount == 0 {
		t.Fatal("socket was not closed after fatal read error")
	}
}

func TestManagerCancellationClosesCurrentSocket(t *testing.T) {
	socket := newFakeSocket()
	factory := &fakeFactory{outcomes: []listenOutcome{{socket: socket}}}
	sink := newRecordingSink()
	manager := newManager("127.0.0.1:29501", factory, waitForContext)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.Run(ctx, sink)
		close(done)
	}()

	select {
	case <-sink.ready:
	case <-time.After(time.Second):
		t.Fatal("socket did not become ready")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if socket.closeCount == 0 {
		t.Fatal("context cancellation did not close socket")
	}
	if sink.resets != 0 {
		t.Fatalf("context cancellation caused %d resets", sink.resets)
	}
}

func TestManagerValidatesAndDeliversIndependentDatagrams(t *testing.T) {
	valid := []byte("$GPGSV,1,1,00*79")
	baseTime := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	reads := []fakeRead{
		readEvent(nil, baseTime),
		readEvent(append([]byte{'$'}, make([]byte, MaxDatagramSize)...), baseTime.Add(time.Second)),
		truncatedEvent([]byte("$A*00"), baseTime.Add(2*time.Second)),
		readEvent([]byte("$A*00\n$B*00"), baseTime.Add(3*time.Second)),
		readEvent([]byte{'$', 'X', '*', '0', '0', 0}, baseTime.Add(4*time.Second)),
		readEvent([]byte("$GPGSV,1,1,00*00"), baseTime.Add(5*time.Second)),
		readEvent(nmeaSentence("GPXYZ,1"), baseTime.Add(6*time.Second)),
		readEventFrom(valid, remoteSource, baseTime.Add(7*time.Second)),
		readEvent(valid, baseTime.Add(8*time.Second)),
		readEvent(append(append([]byte(nil), valid...), '\n'), baseTime.Add(9*time.Second)),
		readEvent(append(append([]byte(nil), valid...), '\r', '\n'), baseTime.Add(10*time.Second)),
		readEvent([]byte("$GPGSV,1,1"), baseTime.Add(11*time.Second)),
		readEvent([]byte(",00*79"), baseTime.Add(12*time.Second)),
		{err: errRead},
	}
	socket := newFakeSocket(reads...)
	sink := runOneSocket(t, socket)

	if len(sink.sentences) != 3 {
		t.Fatalf("delivered sentences = %d, want 3", len(sink.sentences))
	}
	for i, sentence := range sink.sentences {
		wantTime := baseTime.Add(time.Duration(8+i) * time.Second)
		if sentence.Kind != nmea.KindGSV || !sentence.ReceivedAt.Equal(wantTime) {
			t.Fatalf("sentence %d = kind %v time %v, want GSV at %v", i, sentence.Kind, sentence.ReceivedAt, wantTime)
		}
	}
	wantDatagramRejects := map[RejectReason]int{
		RejectEmpty:     1,
		RejectTooLong:   1,
		RejectTruncated: 1,
		RejectMultiple:  1,
		RejectNUL:       1,
		RejectSource:    1,
		RejectStart:     1,
	}
	if !reflect.DeepEqual(sink.datagramRejects, wantDatagramRejects) {
		t.Fatalf("datagram rejects = %#v, want %#v", sink.datagramRejects, wantDatagramRejects)
	}
	if sink.checksumRejects != 2 {
		t.Fatalf("checksum rejects = %d, want 2", sink.checksumRejects)
	}
	if sink.parseRejects != 1 {
		t.Fatalf("parse rejects = %d, want 1", sink.parseRejects)
	}
	if sink.datagrams != 13 || sink.bytes == 0 {
		t.Fatalf("received datagram accounting = %d/%d", sink.datagrams, sink.bytes)
	}
}

func TestManagerReportsRXQOverflowDeltasIncludingWrap(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []uint32
		want   []uint64
	}{
		{name: "increment", values: []uint32{10, 15}, want: []uint64{10, 5}},
		{name: "wrap", values: []uint32{math.MaxUint32 - 1, 1}, want: []uint64{math.MaxUint32 - 1, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			valid := []byte("$GPGSV,1,1,00*79")
			reads := make([]fakeRead, 0, len(tc.values)+1)
			for _, value := range tc.values {
				value := value
				read := readEvent(valid, time.Now())
				read.result.RXQOverflow = &value
				reads = append(reads, read)
			}
			reads = append(reads, fakeRead{err: errRead})
			socket := newFakeSocket(reads...)
			socket.info.DropSource = DropRXQOverflow
			sink := runOneSocket(t, socket)

			var got []uint64
			for _, event := range sink.drops {
				if event.source != DropRXQOverflow {
					t.Fatalf("drop source = %v", event.source)
				}
				got = append(got, event.delta)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("drop deltas = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestManagerSurfacesProcFallbackAndUnavailableTelemetry(t *testing.T) {
	valid := []byte("$GPGSV,1,1,00*79")
	procSocket := newFakeSocket(
		readEvent(valid, time.Now()),
		readEvent(valid, time.Now()),
		fakeRead{err: errRead},
	)
	procSocket.info = SocketInfo{RequestedReadBuffer: ReadBufferSize, ActualReadBuffer: 64 * 1024, DropSource: DropProcUDP}
	procSocket.procSamples = []procSample{{100, true}, {104, true}, {104, true}}
	procSink := runOneSocket(t, procSocket)
	if len(procSink.socketInfos) != 1 || procSink.socketInfos[0] != procSocket.info {
		t.Fatalf("socket info = %#v, want %#v", procSink.socketInfos, procSocket.info)
	}
	if want := []dropEvent{{delta: 4, source: DropProcUDP}}; !reflect.DeepEqual(procSink.drops, want) {
		t.Fatalf("proc drops = %#v, want %#v", procSink.drops, want)
	}

	unavailableSocket := newFakeSocket(fakeRead{err: errRead})
	unavailableSocket.info = SocketInfo{RequestedReadBuffer: ReadBufferSize, ActualReadBuffer: 32 * 1024, DropSource: DropUnavailable}
	unavailableSink := runOneSocket(t, unavailableSocket)
	if len(unavailableSink.socketInfos) != 1 || unavailableSink.socketInfos[0].DropSource != DropUnavailable {
		t.Fatalf("unavailable telemetry not surfaced: %#v", unavailableSink.socketInfos)
	}
}

func runOneSocket(t *testing.T, socket *fakeSocket) *recordingSink {
	t.Helper()
	factory := &fakeFactory{outcomes: []listenOutcome{{socket: socket}}}
	sink := newRecordingSink()
	ctx, cancel := context.WithCancel(context.Background())
	manager := newManager("127.0.0.1:29501", factory, func(context.Context, time.Duration) bool {
		cancel()
		return false
	})
	manager.Run(ctx, sink)
	return sink
}

func readEvent(data []byte, receivedAt time.Time) fakeRead {
	return readEventFrom(data, loopbackSource, receivedAt)
}

func readEventFrom(data []byte, source netip.AddrPort, receivedAt time.Time) fakeRead {
	return fakeRead{
		data: data,
		result: ReadResult{
			N:          len(data),
			Source:     source,
			ReceivedAt: receivedAt,
		},
	}
}

func truncatedEvent(data []byte, receivedAt time.Time) fakeRead {
	read := readEvent(data, receivedAt)
	read.result.Truncated = true
	return read
}

func nmeaSentence(body string) []byte {
	var checksum byte
	for i := 0; i < len(body); i++ {
		checksum ^= body[i]
	}
	return []byte(fmt.Sprintf("$%s*%02X", body, checksum))
}
