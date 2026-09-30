package app

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"testing"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/observe"
	"gnssagent/internal/protocol"
	statusserver "gnssagent/internal/server"
	"gnssagent/internal/timequality"
	"gnssagent/internal/udpinput"
)

func TestLoopbackEndToEndSimpleStatusAndSilentSwitch(t *testing.T) {
	harness := startIntegrationHarness(t)
	defer harness.stop(t)
	client := harness.subscribeClient(t, true)
	defer client.Close()

	sender := harness.udpSender(t)
	defer sender.Close()
	sendDatagram(t, sender, nmeaFrame("GNGGA,123519.00,3112.0000,N,12130.0000,E,1,08,0.9,12.3,M,0.0,M,,"))
	sendDatagram(t, sender, nmeaFrame("GNGGA,123520.00,3112.0000,N,12130.0000,E,1,08,0.9,12.3,M,0.0,M,,"))

	frame := readWireFrame(t, client, time.Second)
	if frame[4] != 2 || frame[5] != protocol.TypeStatusSimple || len(frame) != 72 {
		t.Fatalf("status type/length = %#x/%d, want v2 SIMPLE/72", frame[5], len(frame))
	}
	mask := binary.BigEndian.Uint64(frame[8:16])
	if mask&(model.SimpleLatitudeValid|model.SimpleLongitudeValid|model.SimpleValidValid) != model.SimpleLatitudeValid|model.SimpleLongitudeValid|model.SimpleValidValid {
		t.Fatalf("simple mask = %#x", mask)
	}
	latitude := math.Float64frombits(binary.BigEndian.Uint64(frame[32:40]))
	if math.Abs(latitude-31.2) > 1e-9 {
		t.Fatalf("latitude = %v, want 31.2", latitude)
	}
	if frame[64] != 1 {
		t.Fatalf("valid = %d, want 1", frame[64])
	}
}

func TestLoopbackNoInputSplitDatagramsAndNoFix(t *testing.T) {
	harness := startIntegrationHarness(t)
	defer harness.stop(t)
	client := harness.subscribeClient(t, false)
	defer client.Close()
	assertWireTimeout(t, client, 100*time.Millisecond)

	sender := harness.udpSender(t)
	defer sender.Close()
	line := nmeaFrame("GNGGA,123519.00,3112.0000,N,12130.0000,E,1,08,0.9,12.3,M,0.0,M,,")
	sendDatagram(t, sender, line[:len(line)/2])
	sendDatagram(t, sender, line[len(line)/2:])
	assertWireTimeout(t, client, 100*time.Millisecond)

	sendDatagram(t, sender, nmeaFrame("GNGGA,123520.00,3112.0000,N,12130.0000,E,0,00,127.000,12.3,M,0.0,M,,"))
	sendDatagram(t, sender, nmeaFrame("GNGGA,123521.00,3112.0000,N,12130.0000,E,0,00,127.000,12.3,M,0.0,M,,"))
	frame := readWireFrame(t, client, time.Second)
	mask := binary.BigEndian.Uint64(frame[8:16])
	if mask&model.SimpleValidValid == 0 || frame[64] != 0 {
		t.Fatalf("no-fix simple status mask=%#x valid=%d", mask, frame[64])
	}
}

func TestLoopbackSenderRestartDoesNotLeakPriorPosition(t *testing.T) {
	harness := startIntegrationHarness(t)
	defer harness.stop(t)
	client := harness.subscribeClient(t, false)
	defer client.Close()

	firstSender := harness.udpSender(t)
	sendDatagram(t, firstSender, nmeaFrame("GNGGA,123519.00,3112.0000,N,12130.0000,E,1,08,0.9,12.3,M,0.0,M,,"))
	_ = firstSender.Close()
	_ = readWireFrame(t, client, 3*time.Second)

	secondSender := harness.udpSender(t)
	defer secondSender.Close()
	sendDatagram(t, secondSender, nmeaFrame("GNGGA,123520.00,,,,,1,05,1.0,5.0,M,0.0,M,,"))
	sendDatagram(t, secondSender, nmeaFrame("GNGGA,123521.00,,,,,1,05,1.0,5.0,M,0.0,M,,"))
	frame := readWireFrame(t, client, time.Second)
	mask := binary.BigEndian.Uint64(frame[8:16])
	if mask&(model.SimpleLatitudeValid|model.SimpleLongitudeValid) != 0 {
		t.Fatalf("old sender position leaked into new cycle: mask=%#x", mask)
	}
}

type integrationHarness struct {
	udpAddress string
	tcpAddress string
	cancel     context.CancelFunc
	done       chan error
}

func startIntegrationHarness(t *testing.T, options ...timequality.Config) *integrationHarness {
	t.Helper()
	udpAddress := reserveUDPAddress(t)
	tcpAddress := reserveTCPAddress(t)
	stats := observe.NewStats()
	statusServer := statusserver.New(tcpAddress, 5, 4)
	statusServer.SetObserver(stats)
	manager := udpinput.NewManager(udpAddress)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := New(manager, statusServer, stats, logger, options...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	harness := &integrationHarness{udpAddress: udpAddress, tcpAddress: tcpAddress, cancel: cancel, done: done}

	probe := harness.dialTCP(t)
	_ = probe.Close()
	time.Sleep(30 * time.Millisecond)
	return harness
}

func (h *integrationHarness) subscribeClient(t *testing.T, testSilentSwitch bool) net.Conn {
	t.Helper()
	client := h.dialTCP(t)
	if testSilentSwitch {
		writeWire(t, client, protocol.EncodeSwitchRequest(protocol.SwitchRequest{RequestID: 1, Enabled: 1, Type: protocol.TypeGPSBeiDou}))
		assertWireTimeout(t, client, 30*time.Millisecond)
	}
	writeWire(t, client, protocol.EncodeSubscribeRequest(protocol.StatusSimple))
	ack := readWireFrame(t, client, time.Second)
	if len(ack) != 9 || ack[5] != protocol.TypeSubscribeACK || ack[8] != byte(protocol.SubscribeSuccess) {
		t.Fatalf("subscribe ACK = %x", ack)
	}
	return client
}

func (h *integrationHarness) dialTCP(t *testing.T) net.Conn {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		conn, err := net.DialTimeout("tcp4", h.tcpAddress, 100*time.Millisecond)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial TCP %s: %v", h.tcpAddress, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *integrationHarness) udpSender(t *testing.T) net.Conn {
	t.Helper()
	conn, err := net.Dial("udp4", h.udpAddress)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func (h *integrationHarness) stop(t *testing.T) {
	t.Helper()
	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("integration app stopped with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("integration app did not stop")
	}
}

func reserveUDPAddress(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := conn.LocalAddr().String()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func nmeaFrame(body string) []byte {
	var checksum byte
	for i := 0; i < len(body); i++ {
		checksum ^= body[i]
	}
	return []byte(fmt.Sprintf("$%s*%02X\r\n", body, checksum))
}

func sendDatagram(t *testing.T, conn net.Conn, data []byte) {
	t.Helper()
	if n, err := conn.Write(data); err != nil || n != len(data) {
		t.Fatalf("UDP Write = (%d, %v), want %d", n, err, len(data))
	}
}

func writeWire(t *testing.T, conn net.Conn, data []byte) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, err := conn.Write(data); err != nil || n != len(data) {
		t.Fatalf("TCP Write = (%d, %v), want %d", n, err, len(data))
	}
}

func readWireFrame(t *testing.T, conn net.Conn, timeout time.Duration) []byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, protocol.HeaderSize)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatalf("read header: %v", err)
	}
	length := int(binary.BigEndian.Uint16(header[6:8]))
	frame := append([]byte(nil), header...)
	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	frame = append(frame, payload...)
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	return frame
}

func assertWireTimeout(t *testing.T, conn net.Conn, timeout time.Duration) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	_, err := conn.Read(make([]byte, 1))
	var netError net.Error
	if !errors.As(err, &netError) || !netError.Timeout() {
		t.Fatalf("read error = %v, want timeout", err)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
}
