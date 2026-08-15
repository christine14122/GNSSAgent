//go:build linux

package udpinput

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

type systemSocketFactory struct{}

func newSystemSocketFactory() socketFactory {
	return systemSocketFactory{}
}

func (systemSocketFactory) Listen(address string) (packetSocket, error) {
	packetConn, err := net.ListenPacket("udp4", address)
	if err != nil {
		return nil, err
	}
	udpConn, ok := packetConn.(*net.UDPConn)
	if !ok {
		_ = packetConn.Close()
		return nil, fmt.Errorf("udp4 listener has unexpected type %T", packetConn)
	}
	if err := udpConn.SetReadBuffer(ReadBufferSize); err != nil {
		_ = udpConn.Close()
		return nil, fmt.Errorf("set UDP receive buffer: %w", err)
	}

	rawConn, err := udpConn.SyscallConn()
	if err != nil {
		_ = udpConn.Close()
		return nil, fmt.Errorf("access UDP socket: %w", err)
	}

	info := SocketInfo{RequestedReadBuffer: ReadBufferSize}
	var kernelReadBuffer int
	var inode uint64
	var socketErr error
	err = rawConn.Control(func(fd uintptr) {
		kernelReadBuffer, socketErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF)
		if socketErr != nil {
			return
		}
		info.KernelReadBuffer = kernelReadBuffer
		info.ActualReadBuffer = kernelReadBuffer / 2

		var stat unix.Stat_t
		if err := unix.Fstat(int(fd), &stat); err == nil {
			inode = stat.Ino
		}
		if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RXQ_OVFL, 1); err == nil {
			info.DropSource = DropRXQOverflow
		} else if inode != 0 {
			info.DropSource = DropProcUDP
		}
	})
	if err != nil {
		_ = udpConn.Close()
		return nil, fmt.Errorf("configure UDP socket: %w", err)
	}
	if socketErr != nil {
		_ = udpConn.Close()
		return nil, fmt.Errorf("read UDP receive buffer: %w", socketErr)
	}

	return &linuxPacketSocket{
		conn:  udpConn,
		info:  info,
		inode: inode,
		oob:   make([]byte, unix.CmsgSpace(4)),
	}, nil
}

type linuxPacketSocket struct {
	conn  *net.UDPConn
	info  SocketInfo
	inode uint64
	oob   []byte
}

func (s *linuxPacketSocket) Read(buffer []byte) (ReadResult, error) {
	n, oobn, flags, source, err := s.conn.ReadMsgUDP(buffer, s.oob)
	receivedAt := time.Now()
	result := ReadResult{
		N:          n,
		Truncated:  flags&unix.MSG_TRUNC != 0,
		ReceivedAt: receivedAt,
	}
	if source != nil {
		result.Source = source.AddrPort()
	}
	if err == nil && oobn > 0 {
		result.RXQOverflow = parseRXQOverflow(s.oob[:oobn])
	}
	return result, err
}

func parseRXQOverflow(oob []byte) *uint32 {
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil
	}
	for _, message := range messages {
		if message.Header.Level == unix.SOL_SOCKET && message.Header.Type == unix.SO_RXQ_OVFL && len(message.Data) >= 4 {
			value := binary.NativeEndian.Uint32(message.Data[:4])
			return &value
		}
	}
	return nil
}

func (s *linuxPacketSocket) ProcDrops() (uint64, bool) {
	if s.info.DropSource != DropProcUDP || s.inode == 0 {
		return 0, false
	}
	return readProcUDPDrops(s.inode)
}

func (s *linuxPacketSocket) Info() SocketInfo {
	return s.info
}

func (s *linuxPacketSocket) Close() error {
	return s.conn.Close()
}
