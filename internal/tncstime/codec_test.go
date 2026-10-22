package tncstime

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestProto3GoldenFrames(t *testing.T) {
	for _, tc := range []struct {
		req request
		hex string
	}{
		{request{}, "0000000408909506"},
		{request{timestamp: 1, trusted: true}, "0000000a08909506120408011001"},
	} {
		want, _ := hex.DecodeString(tc.hex)
		if got := encodeRequest(tc.req); !bytes.Equal(got, want) {
			t.Fatalf("frame=%x want=%x", got, want)
		}
	}
	if err := decodeResponse(successFrame[4:]); err != nil {
		t.Fatal(err)
	}
	// success defaults to false, and unknown fixed32, fixed64, bytes and varint fields are skipped.
	rejected := []byte{0x08, 0xb0, 0xa2, 0x0c, 0x12, 0x04, 0x12, 0x02, 'n', 'o'}
	rejected = append(rejected, 0x18, 1, 0x25, 1, 2, 3, 4, 0x29, 0, 0, 0, 0, 0, 0, 0, 0, 0x32, 1, 0)
	var refusal *rejection
	if err := decodeResponse(rejected); !errors.As(err, &refusal) || refusal.reason != "no" {
		t.Fatalf("rejection=%v", err)
	}
}

func TestMalformedResponses(t *testing.T) {
	for _, data := range [][]byte{
		nil, {0}, {0x08, 0x80}, {0x08, 1},
		{0x08, 0xb0, 0xa2, 0x0c, 0x12, 4, 0x08},
		{0x08, 0xb0, 0xa2, 0x0c, 0x12, 2, 0x0a, 0},
		{0x08, 0xb0, 0xa2, 0x0c, 0x12, 3, 0x12, 1, 0xff},
	} {
		if err := decodeResponse(data); err == nil {
			t.Fatalf("accepted malformed response %x", data)
		}
	}
}

type shortWriter struct{ net.Conn }

func (w shortWriter) Write(p []byte) (int, error) {
	if len(p) > 2 {
		p = p[:2]
	}
	return w.Conn.Write(p)
}

func TestExchangeHandlesPartialWritesAndFragmentedResponse(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	errCh := make(chan error, 1)
	go func() {
		r, err := readRequest(server)
		if err == nil && r != (request{timestamp: 123, trusted: true}) {
			err = errors.New("incorrect request")
		}
		if err == nil {
			for _, b := range successFrame {
				if _, err = server.Write([]byte{b}); err != nil {
					break
				}
			}
		}
		errCh <- err
	}()
	if err := exchange(context.Background(), shortWriter{client}, request{123, true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestExchangeRejectsOversizedFrame(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() { readRequest(server); server.Write([]byte{0xff, 0xff, 0xff, 0xff}) }()
	if err := exchange(context.Background(), client, request{}, nil); err == nil {
		t.Fatal("accepted oversized response")
	}
}

func TestExchangeCancellationInterruptsBlockedResponse(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { readRequest(server); cancel() }()
	started := time.Now()
	if err := exchange(ctx, client, request{}, nil); err == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(started) > time.Second {
		t.Fatal("shutdown waited for I/O timeout")
	}
}

func TestExchangeTruncatedResponse(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() { defer server.Close(); readRequest(server); server.Write(successFrame[:6]) }()
	if err := exchange(context.Background(), client, request{}, nil); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error=%v", err)
	}
}
