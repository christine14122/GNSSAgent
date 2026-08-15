//go:build linux

package udpinput

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxSocketReportsKernelAndEffectiveReadBuffer(t *testing.T) {
	socket, err := newSystemSocketFactory().Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()

	info := socket.Info()
	if info.ActualReadBuffer <= 0 || info.KernelReadBuffer <= 0 {
		t.Fatalf("socket buffer info = %+v", info)
	}
	if info.KernelReadBuffer != info.ActualReadBuffer*2 {
		t.Fatalf("Linux kernel/effective SO_RCVBUF = %d/%d, want 2:1", info.KernelReadBuffer, info.ActualReadBuffer)
	}
}

func TestLinuxSocketMarksOversizedDatagramTruncated(t *testing.T) {
	socket, err := newSystemSocketFactory().Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	linuxSocket := socket.(*linuxPacketSocket)
	if err := linuxSocket.conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	sender, err := net.DialUDP("udp4", nil, linuxSocket.conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if _, err := sender.Write(bytes.Repeat([]byte{'$'}, 2000)); err != nil {
		t.Fatal(err)
	}

	buffer := make([]byte, MaxDatagramSize+1)
	result, err := socket.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatalf("oversized datagram result = %+v", result)
	}
	if _, reason := NormalizeDatagram(buffer, result.N, result.Truncated); reason != RejectTruncated {
		t.Fatalf("normalization reason = %v, want %v", reason, RejectTruncated)
	}
}

func TestParseRXQOverflowControlMessage(t *testing.T) {
	header := unix.Cmsghdr{Level: unix.SOL_SOCKET, Type: unix.SO_RXQ_OVFL}
	header.SetLen(unix.CmsgLen(4))
	var encoded bytes.Buffer
	if err := binary.Write(&encoded, binary.NativeEndian, header); err != nil {
		t.Fatal(err)
	}
	oob := make([]byte, unix.CmsgSpace(4))
	copy(oob, encoded.Bytes())
	binary.NativeEndian.PutUint32(oob[unix.CmsgLen(0):], 0x10203040)

	value := parseRXQOverflow(oob)
	if value == nil || *value != 0x10203040 {
		t.Fatalf("overflow value = %v", value)
	}
}
