//go:build !linux

package udpinput

import (
	"net"
	"time"
)

type systemSocketFactory struct{}

func newSystemSocketFactory() socketFactory {
	return systemSocketFactory{}
}

func (systemSocketFactory) Listen(address string) (packetSocket, error) {
	conn, err := net.ListenPacket("udp4", address)
	if err != nil {
		return nil, err
	}
	if udpConn, ok := conn.(*net.UDPConn); ok {
		_ = udpConn.SetReadBuffer(ReadBufferSize)
	}
	return &portablePacketSocket{
		conn: conn,
		info: SocketInfo{
			RequestedReadBuffer: ReadBufferSize,
			DropSource:          DropUnavailable,
		},
	}, nil
}

type portablePacketSocket struct {
	conn net.PacketConn
	info SocketInfo
}

func (s *portablePacketSocket) Read(buffer []byte) (ReadResult, error) {
	n, source, err := s.conn.ReadFrom(buffer)
	receivedAt := time.Now()
	result := ReadResult{N: n, ReceivedAt: receivedAt}
	if udpSource, ok := source.(*net.UDPAddr); ok {
		result.Source = udpSource.AddrPort()
	}
	return result, err
}

func (s *portablePacketSocket) ProcDrops() (uint64, bool) {
	return 0, false
}

func (s *portablePacketSocket) Info() SocketInfo {
	return s.info
}

func (s *portablePacketSocket) Close() error {
	return s.conn.Close()
}
