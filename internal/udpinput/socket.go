package udpinput

import (
	"net/netip"
	"time"
)

type DropSource uint8

const (
	DropUnavailable DropSource = iota
	DropRXQOverflow
	DropProcUDP
)

type SocketInfo struct {
	RequestedReadBuffer int
	ActualReadBuffer    int
	DropSource          DropSource
}

type ReadResult struct {
	N           int
	Source      netip.AddrPort
	Truncated   bool
	RXQOverflow *uint32
	ReceivedAt  time.Time
}

type packetSocket interface {
	Read([]byte) (ReadResult, error)
	ProcDrops() (uint64, bool)
	Info() SocketInfo
	Close() error
}

type socketFactory interface {
	Listen(address string) (packetSocket, error)
}
