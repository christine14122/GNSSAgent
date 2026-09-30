package server

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/protocol"
)

func TestSessionFirstSubscriptionWins(t *testing.T) {
	client, session, hub := startPipeSession(t, 200*time.Millisecond, 200*time.Millisecond)
	defer client.Close()

	writeAll(t, client, protocol.EncodeSubscribeRequest(protocol.StatusSimple))
	assertACK(t, client, protocol.SubscribeSuccess)
	writeAll(t, client, protocol.EncodeSubscribeRequest(protocol.StatusFull))
	assertACK(t, client, protocol.SubscribeAlreadySubscribed)

	hub.Publish(model.FullStatus{FieldValidityMask: model.FullValidValid, Valid: 1})
	frame := readFrame(t, client, time.Second)
	if frame.Type != protocol.TypeStatusSimple || len(frame.Payload) != 64 {
		t.Fatalf("status frame type=%#x payload=%d, want SIMPLE/64", frame.Type, len(frame.Payload))
	}
	if session.selectedStatusType() != protocol.StatusSimple {
		t.Fatalf("session status type = %v, want SIMPLE", session.selectedStatusType())
	}
}

func TestSessionRejectsInvalidStatusAndUnsupportedVersion(t *testing.T) {
	client, _, _ := startPipeSession(t, 200*time.Millisecond, 200*time.Millisecond)
	defer client.Close()

	writeAll(t, client, rawFrame(protocol.Version, protocol.TypeSubscribeRequest, []byte{0xff}))
	assertACK(t, client, protocol.SubscribeInvalidStatusType)
	writeAll(t, client, rawFrame(protocol.Version+1, protocol.TypeSubscribeRequest, []byte{byte(protocol.StatusFull)}))
	assertACK(t, client, protocol.SubscribeUnsupportedVersion)
	writeAll(t, client, protocol.EncodeSubscribeRequest(protocol.StatusFull))
	assertACK(t, client, protocol.SubscribeSuccess)
}

func TestSessionSkipsGarbageUnknownAndClientACKFrames(t *testing.T) {
	client, _, _ := startPipeSession(t, 200*time.Millisecond, 200*time.Millisecond)
	defer client.Close()

	input := append([]byte("garbage"), rawFrame(protocol.Version, 0x7f, []byte{1, 2, 3})...)
	input = append(input, rawFrame(protocol.Version, protocol.TypeSwitchACK, make([]byte, 5))...)
	input = append(input, protocol.EncodeSubscribeRequest(protocol.StatusSimple)...)
	writeAll(t, client, input)
	assertACK(t, client, protocol.SubscribeSuccess)
}

func TestSessionSubscriptionTimeoutClosesConnection(t *testing.T) {
	if defaultSubscriptionTimeout != 5*time.Second {
		t.Fatalf("production subscription timeout = %v, want 5s", defaultSubscriptionTimeout)
	}
	client, session, _ := startPipeSession(t, 30*time.Millisecond, 200*time.Millisecond)
	defer client.Close()

	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("session did not close after subscription timeout")
	}
	buffer := make([]byte, 1)
	if _, err := client.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatalf("client read error = %v, want EOF", err)
	}
}

func TestSessionSilentlySkipsSwitchRequests(t *testing.T) {
	requests := []struct {
		name  string
		frame []byte
	}{
		{
			name: "canonical",
			frame: protocol.EncodeSwitchRequest(protocol.SwitchRequest{
				RequestID: 7,
				Enabled:   1,
				Type:      protocol.TypeGPSBeiDou,
			}),
		},
		{name: "wrong version-one payload length", frame: rawFrame(protocol.Version, protocol.TypeSwitchRequest, []byte{1, 2, 3, 4})},
	}

	for _, tc := range requests {
		t.Run(tc.name, func(t *testing.T) {
			client, session, _ := startPipeSession(t, time.Second, 200*time.Millisecond)
			defer client.Close()

			writeAll(t, client, tc.frame)
			assertReadTimeout(t, client, 30*time.Millisecond)
			if session.isSubscribed() {
				t.Fatal("switch request changed subscription state")
			}
			writeAll(t, client, protocol.EncodeSubscribeRequest(protocol.StatusSimple))
			assertACK(t, client, protocol.SubscribeSuccess)
		})
	}
}

func TestHubReplacesPendingStatusWithLatest(t *testing.T) {
	hub := NewHub()
	subscriber := newSubscriber(protocol.StatusFull)
	hub.add(subscriber)
	defer hub.remove(subscriber)

	first := model.FullStatus{FieldValidityMask: model.FullUsedSatellitesValid, UsedSatellites: 3}
	latest := model.FullStatus{FieldValidityMask: model.FullUsedSatellitesValid, UsedSatellites: 9}
	hub.Publish(first)
	hub.Publish(latest)

	got := <-subscriber.queue
	if got != latest {
		t.Fatalf("pending status is not the latest value")
	}
}

func TestSessionEncodesSubscriberFormat(t *testing.T) {
	for _, statusType := range []protocol.StatusType{protocol.StatusSimple, protocol.StatusFull} {
		client, _, hub := startPipeSession(t, time.Second, time.Second)
		writeAll(t, client, protocol.EncodeSubscribeRequest(statusType))
		assertACK(t, client, protocol.SubscribeSuccess)
		hub.Publish(model.FullStatus{})
		frame := readFrame(t, client, time.Second)
		_ = client.Close()
		wantType, size := protocol.TypeStatusSimple, 64
		if statusType == protocol.StatusFull {
			wantType, size = protocol.TypeStatusFull, 136
		}
		if frame.Type != wantType || len(frame.Payload) != size {
			t.Fatalf("status frame type=%#x payload=%d, want %#x/%d", frame.Type, len(frame.Payload), wantType, size)
		}
	}
}

func TestBlockedSubscriberCannotBlockPublishOrAnotherSubscriber(t *testing.T) {
	hub := NewHub()
	blocked := newSubscriber(protocol.StatusFull)
	active := newSubscriber(protocol.StatusSimple)
	hub.add(blocked)
	hub.add(active)
	defer hub.remove(blocked)
	defer hub.remove(active)

	hub.Publish(model.FullStatus{FieldValidityMask: model.FullUsedSatellitesValid, UsedSatellites: 1})
	<-active.queue

	done := make(chan struct{})
	go func() {
		hub.Publish(model.FullStatus{FieldValidityMask: model.FullUsedSatellitesValid, UsedSatellites: 2})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Hub.Publish blocked on a full subscriber queue")
	}
	select {
	case status := <-active.queue:
		if status.UsedSatellites != 2 {
			t.Fatalf("active subscriber satellites = %d, want 2", status.UsedSatellites)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("active subscriber did not receive publication")
	}
}

func TestWriteDeadlineClosesOnlyBlockedSession(t *testing.T) {
	if defaultWriteTimeout != 3*time.Second {
		t.Fatalf("production write timeout = %v, want 3s", defaultWriteTimeout)
	}
	hub := NewHub()
	blockedClient, blockedSession := startPipeSessionWithHub(t, hub, time.Second, 30*time.Millisecond)
	defer blockedClient.Close()
	activeClient, activeSession := startPipeSessionWithHub(t, hub, time.Second, 200*time.Millisecond)
	defer activeClient.Close()

	writeAll(t, blockedClient, protocol.EncodeSubscribeRequest(protocol.StatusFull))
	assertACK(t, blockedClient, protocol.SubscribeSuccess)
	writeAll(t, activeClient, protocol.EncodeSubscribeRequest(protocol.StatusSimple))
	assertACK(t, activeClient, protocol.SubscribeSuccess)

	hub.Publish(model.FullStatus{})
	frame := readFrame(t, activeClient, time.Second)
	if frame.Type != protocol.TypeStatusSimple {
		t.Fatalf("active frame type = %#x", frame.Type)
	}
	select {
	case <-blockedSession.done:
	case <-time.After(time.Second):
		t.Fatal("blocked session was not closed by write deadline")
	}
	select {
	case <-activeSession.done:
		t.Fatal("blocked session closed the active session")
	default:
	}
}

func TestServerPackageHasNoControlDependency(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "internal/control") {
			t.Fatalf("%s imports internal/control", entry.Name())
		}
	}
}

func TestServerRejectsOverCapacityWithoutSubscriptionWait(t *testing.T) {
	server := newServer("unused", 1, 0, net.Listen, func(net.Conn, []byte) (int, bool) {
		return 0, false
	})
	serverConn, client := net.Pipe()
	defer client.Close()
	if err := client.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	go server.rejectFull(serverConn)
	buffer := make([]byte, 1)
	if _, err := client.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatalf("rejected client read error = %v, want immediate EOF", err)
	}
}

func TestServerReturnsFullForAlreadyBufferedLegalSubscribe(t *testing.T) {
	buffered := protocol.EncodeSubscribeRequest(protocol.StatusSimple)
	server := newServer("unused", 1, 0, net.Listen, func(_ net.Conn, destination []byte) (int, bool) {
		return copy(destination, buffered), true
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()
	client, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var serverConn net.Conn
	select {
	case serverConn = <-accepted:
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("loopback test connection was not accepted")
	}
	go server.rejectFull(serverConn)
	assertACK(t, client, protocol.SubscribeServerFull)
}

func TestFourRemoteSubscribersDoNotBlockFifthLoopback(t *testing.T) {
	listener := newFakeListener()
	server := newServer("unused", 5, 4, func(string, string) (net.Listener, error) {
		return listener, nil
	}, func(net.Conn, []byte) (int, bool) {
		return 0, false
	})
	cancel, done := runTestServer(t, server)
	defer func() {
		cancel()
		<-done
	}()

	var remoteClients []net.Conn
	defer func() {
		for _, client := range remoteClients {
			_ = client.Close()
		}
	}()
	for i := 0; i < 4; i++ {
		client := listener.connect(t, remoteSourceAddr)
		remoteClients = append(remoteClients, client)
		writeAll(t, client, protocol.EncodeSubscribeRequest(protocol.StatusSimple))
		assertACK(t, client, protocol.SubscribeSuccess)
	}
	server.Publish(model.FullStatus{})
	for i, client := range remoteClients {
		if frame := readFrame(t, client, time.Second); frame.Type != protocol.TypeStatusSimple {
			t.Fatalf("remote %d did not continuously drain SIMPLE status", i)
		}
	}

	rejected := listener.connect(t, remoteSourceAddr)
	if err := rejected.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := rejected.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("fifth remote read error = %v, want EOF", err)
	}
	_ = rejected.Close()

	loopback := listener.connect(t, loopbackSourceAddr)
	defer loopback.Close()
	writeAll(t, loopback, protocol.EncodeSubscribeRequest(protocol.StatusFull))
	assertACK(t, loopback, protocol.SubscribeSuccess)
}

func TestServerReturnsListenFailure(t *testing.T) {
	want := errors.New("listen failed")
	server := newServer("unused", 5, 4, func(string, string) (net.Listener, error) {
		return nil, want
	}, tryReadBuffered)
	if err := server.Run(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Run error = %v, want %v", err, want)
	}
}

func TestServerObserverReceivesLifecycleEvents(t *testing.T) {
	listener := newFakeListener()
	server := newServer("unused", 2, 1, func(string, string) (net.Listener, error) {
		return listener, nil
	}, func(net.Conn, []byte) (int, bool) { return 0, false })
	observer := &recordingObserver{}
	server.SetObserver(observer)
	cancel, done := runTestServer(t, server)
	defer func() {
		cancel()
		<-done
	}()

	active := listener.connect(t, remoteSourceAddr)
	writeAll(t, active, protocol.EncodeSubscribeRequest(protocol.StatusFull))
	assertACK(t, active, protocol.SubscribeSuccess)
	rejected := listener.connect(t, remoteSourceAddr)
	defer rejected.Close()
	if err := rejected.SetReadDeadline(time.Now().Add(time.Second)); err == nil {
		_, _ = rejected.Read(make([]byte, 1))
	}

	server.Publish(model.FullStatus{})
	time.Sleep(10 * time.Millisecond)
	server.Publish(model.FullStatus{FieldValidityMask: model.FullUsedSatellitesValid, UsedSatellites: 1})
	server.Publish(model.FullStatus{FieldValidityMask: model.FullUsedSatellitesValid, UsedSatellites: 2})
	waitObserver(t, observer, func(snapshot observerSnapshot) bool {
		return snapshot.connections == 2 && snapshot.rejections == 1 && snapshot.fullSubscriptions == 1 && snapshot.replacements >= 1
	})
	_ = active.Close()
	waitObserver(t, observer, func(snapshot observerSnapshot) bool {
		return snapshot.disconnections == 2
	})
}

func startPipeSession(t *testing.T, subscribeTimeout, writeTimeout time.Duration) (net.Conn, *session, *Hub) {
	t.Helper()
	hub := NewHub()
	client, session := startPipeSessionWithHub(t, hub, subscribeTimeout, writeTimeout)
	return client, session, hub
}

func startPipeSessionWithHub(t *testing.T, hub *Hub, subscribeTimeout, writeTimeout time.Duration) (net.Conn, *session) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	session := newSession(serverConn, hub, subscribeTimeout, writeTimeout)
	go session.run()
	return clientConn, session
}

func assertACK(t *testing.T, conn net.Conn, want protocol.SubscribeResult) {
	t.Helper()
	frame := readFrame(t, conn, time.Second)
	if frame.Type != protocol.TypeSubscribeACK {
		t.Fatalf("frame type = %#x, want subscribe ACK", frame.Type)
	}
	got, err := protocol.ParseSubscribeACK(frame.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ACK = %v, want %v", got, want)
	}
}

func assertReadTimeout(t *testing.T, conn net.Conn, timeout time.Duration) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	_, err := conn.Read(buffer)
	var netError net.Error
	if !errors.As(err, &netError) || !netError.Timeout() {
		t.Fatalf("read error = %v, want timeout (not EOF or data)", err)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func readFrame(t *testing.T, conn net.Conn, timeout time.Duration) protocol.Frame {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, protocol.HeaderSize)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	if string(header[:4]) != protocol.Magic {
		t.Fatalf("bad magic %q", header[:4])
	}
	payload := make([]byte, int(binary.BigEndian.Uint16(header[6:8])))
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatalf("read frame payload: %v", err)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	return protocol.Frame{Version: header[4], Type: header[5], Payload: payload}
}

func writeAll(t *testing.T, conn net.Conn, data []byte) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func rawFrame(version, messageType uint8, payload []byte) []byte {
	frame := make([]byte, protocol.HeaderSize+len(payload))
	copy(frame[:4], protocol.Magic)
	frame[4] = version
	frame[5] = messageType
	binary.BigEndian.PutUint16(frame[6:8], uint16(len(payload)))
	copy(frame[8:], payload)
	return frame
}

var (
	remoteSourceAddr   = &net.TCPAddr{IP: net.ParseIP("192.168.7.2"), Port: 31000}
	loopbackSourceAddr = &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 31000}
)

type addressedConn struct {
	net.Conn
	remote net.Addr
}

func (c *addressedConn) RemoteAddr() net.Addr {
	return c.remote
}

type fakeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	closeOnce   sync.Once
}

func newFakeListener() *fakeListener {
	return &fakeListener{
		connections: make(chan net.Conn),
		closed:      make(chan struct{}),
	}
}

func (l *fakeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *fakeListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *fakeListener) Addr() net.Addr {
	return loopbackSourceAddr
}

func (l *fakeListener) connect(t *testing.T, remote net.Addr) net.Conn {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	wrapped := &addressedConn{Conn: serverConn, remote: remote}
	select {
	case l.connections <- wrapped:
	case <-time.After(time.Second):
		t.Fatal("server did not accept test connection")
	}
	return clientConn
}

func runTestServer(t *testing.T, server *Server) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	return cancel, done
}

type observerSnapshot struct {
	connections         int
	disconnections      int
	rejections          int
	simpleSubscriptions int
	fullSubscriptions   int
	replacements        int
}

type recordingObserver struct {
	mu       sync.Mutex
	snapshot observerSnapshot
}

func (o *recordingObserver) RecordTCPConnection() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.snapshot.connections++
}

func (o *recordingObserver) RecordTCPRejection() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.snapshot.rejections++
}

func (o *recordingObserver) RecordTCPDisconnection() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.snapshot.disconnections++
}

func (o *recordingObserver) RecordTCPSubscription(statusType protocol.StatusType) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if statusType == protocol.StatusSimple {
		o.snapshot.simpleSubscriptions++
	} else if statusType == protocol.StatusFull {
		o.snapshot.fullSubscriptions++
	}
}

func (o *recordingObserver) RecordSlowClientReplacement() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.snapshot.replacements++
}

func (o *recordingObserver) current() observerSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.snapshot
}

func waitObserver(t *testing.T, observer *recordingObserver, condition func(observerSnapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition(observer.current()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("observer condition not met: %+v", observer.current())
}
