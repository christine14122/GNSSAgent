package server

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"gnssagent/internal/protocol"
)

func TestExpiredPartialStatusClosesBeforeQueuedACK(t *testing.T) {
	for _, statusType := range []protocol.StatusType{protocol.StatusSimple, protocol.StatusFull} {
		name, frameSize := "simple", protocol.HeaderSize+64
		if statusType == protocol.StatusFull {
			name, frameSize = "full", protocol.HeaderSize+136
		}
		t.Run(name, func(t *testing.T) {
			serverConn, client := net.Pipe()
			defer client.Close()
			conn := &failedWriteReporter{Conn: serverConn, failed: make(chan error, 1)}
			hub := NewHub()
			session := newSession(conn, hub, time.Second, 3*time.Second)
			t.Cleanup(func() {
				select {
				case <-session.done:
				case <-time.After(time.Second):
					t.Error("session did not finish after cleanup was unblocked")
				}
			})
			go session.run()
			writeAll(t, client, protocol.EncodeSubscribeRequest(statusType))
			assertACK(t, client, protocol.SubscribeSuccess)
			if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}

			status := qualityStatus(12345)
			status.TimeQuality.ExpiresAt = time.Now().Add(100 * time.Millisecond)
			hub.Publish(status)
			if _, err := io.ReadFull(client, make([]byte, frameSize-4)); err != nil {
				t.Fatal(err)
			}
			// The read loop consumes this request while its ACK waits for writeMu.
			writeAll(t, client, protocol.EncodeSubscribeRequest(statusType))
			// Hold cleanup behind hub.remove; the socket must close independently.
			hub.mu.Lock()
			defer hub.mu.Unlock()
			select {
			case err := <-conn.failed:
				var timeout net.Error
				if !errors.As(err, &timeout) || !timeout.Timeout() {
					t.Fatalf("partial write error = %v, want deadline timeout", err)
				}
			case <-time.After(time.Second):
				t.Fatal("partial trusted frame outlived its quality deadline")
			}
			if n, err := client.Read(make([]byte, 4)); n != 0 || !errors.Is(err, io.EOF) {
				t.Fatalf("read after failed partial frame = %d bytes, %v; want EOF without ACK bytes", n, err)
			}
			select {
			case <-session.done:
				t.Fatal("session cleanup completed while hub.mu was locked")
			default:
			}
		})
	}
}

type failedWriteReporter struct {
	net.Conn
	failed chan error
}

func (c *failedWriteReporter) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err != nil {
		select {
		case c.failed <- err:
		default:
		}
	}
	return n, err
}

func TestSessionWriteFailurePreventsFurtherFrames(t *testing.T) {
	writeErr := errors.New("write failed")
	for _, tc := range []struct {
		name        string
		deadlineErr error
		writeErr    error
		writeLimit  int
		wantErr     error
	}{
		{name: "deadline", deadlineErr: writeErr, wantErr: writeErr},
		{name: "write", writeErr: writeErr, wantErr: writeErr},
		{name: "partial write", writeErr: writeErr, writeLimit: 2, wantErr: writeErr},
		{name: "zero write", wantErr: io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &faultWriteConn{deadlineErr: tc.deadlineErr, writeErr: tc.writeErr, writeLimit: tc.writeLimit}
			session := newSession(conn, NewHub(), time.Second, time.Second)
			if err := session.writeFrame([]byte{1, 2, 3}); !errors.Is(err, tc.wantErr) {
				t.Fatalf("write error = %v, want %v", err, tc.wantErr)
			}
			if !conn.closed {
				t.Error("failed write did not close the connection before returning")
			}
			writes, deadlines, size := conn.writes, conn.deadlines, conn.output.Len()
			// Even a transport that accepts writes after Close must not be reused.
			conn.deadlineErr, conn.writeErr, conn.writeLimit = nil, nil, 1000
			if session.writeACK(protocol.SubscribeAlreadySubscribed) {
				t.Error("ACK succeeded after a failed frame")
			}
			for _, statusType := range []protocol.StatusType{protocol.StatusSimple, protocol.StatusFull} {
				if err := session.writeStatus(qualityStatus(12345), statusType); !errors.Is(err, net.ErrClosed) {
					t.Errorf("status write error = %v, want net.ErrClosed", err)
				}
			}
			if conn.writes != writes || conn.deadlines != deadlines || conn.output.Len() != size {
				t.Fatalf("failed connection reused: writes %d->%d, deadlines %d->%d, bytes %d->%d", writes, conn.writes, deadlines, conn.deadlines, size, conn.output.Len())
			}
		})
	}
}

func TestSessionCompletesShortWritesWithoutError(t *testing.T) {
	conn := &faultWriteConn{writeLimit: 2}
	session := newSession(conn, NewHub(), time.Second, time.Second)
	frame := protocol.EncodeSubscribeACK(protocol.SubscribeSuccess)
	for i := 0; i < 2; i++ {
		if err := session.writeFrame(frame); err != nil {
			t.Fatal(err)
		}
	}
	if conn.closed || !bytes.Equal(conn.output.Bytes(), bytes.Repeat(frame, 2)) {
		t.Fatalf("short writes closed=%v, bytes=%x", conn.closed, conn.output.Bytes())
	}
}

type faultWriteConn struct {
	net.Conn
	deadlineErr error
	writeErr    error
	writeLimit  int
	output      bytes.Buffer
	writes      int
	deadlines   int
	closed      bool
}

func (c *faultWriteConn) Write(p []byte) (int, error) {
	c.writes++
	n := min(len(p), c.writeLimit)
	_, _ = c.output.Write(p[:n])
	return n, c.writeErr
}

func (c *faultWriteConn) SetWriteDeadline(time.Time) error {
	c.deadlines++
	return c.deadlineErr
}

func (c *faultWriteConn) Close() error {
	c.closed = true
	return nil
}
