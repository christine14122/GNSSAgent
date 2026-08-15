//go:build linux

package server

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestTryReadBufferedUsesNonblockingPeekPath(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	client, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	serverConn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()

	started := time.Now()
	if n, ok := tryReadBuffered(serverConn, make([]byte, 64)); ok || n != 0 {
		t.Fatalf("empty socket returned n=%d ok=%v", n, ok)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("empty nonblocking read took %v", elapsed)
	}

	payload := []byte("buffered-subscribe-request")
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	deadline := time.Now().Add(time.Second)
	for {
		n, ok := tryReadBuffered(serverConn, buffer)
		if ok {
			if !bytes.Equal(buffer[:n], payload) {
				t.Fatalf("buffered payload = %q", buffer[:n])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("buffered payload was not observed")
		}
		time.Sleep(time.Millisecond)
	}
}
