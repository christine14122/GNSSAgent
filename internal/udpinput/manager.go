package udpinput

import (
	"context"
	"net/netip"
	"time"

	"gnssagent/internal/nmea"
)

type Sink interface {
	Sentence(nmea.Sentence)
	Reset()
	DatagramRejected(RejectReason)
	NMEARejected(checksum bool)
	KernelDrops(delta uint64, source DropSource)
	SocketReady(SocketInfo)
}

type sleepFunc func(context.Context, time.Duration) bool

type Manager struct {
	address string
	factory socketFactory
	sleep   sleepFunc
}

func NewManager(address string) *Manager {
	return newManager(address, newSystemSocketFactory(), waitForContext)
}

func newManager(address string, factory socketFactory, sleep sleepFunc) *Manager {
	return &Manager{address: address, factory: factory, sleep: sleep}
}

func (m *Manager) Run(ctx context.Context, sink Sink) error {
	retry := newBackoff()
	for ctx.Err() == nil {
		socket, err := m.factory.Listen(m.address)
		if err != nil {
			if !m.sleep(ctx, retry.Next()) {
				return nil
			}
			continue
		}

		retry.Reset()
		if ctx.Err() != nil {
			_ = socket.Close()
			return nil
		}
		sink.SocketReady(socket.Info())

		stopClose := context.AfterFunc(ctx, func() {
			_ = socket.Close()
		})
		err = consumeSocket(socket, sink)
		stopClose()
		_ = socket.Close()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			sink.Reset()
		}
		if !m.sleep(ctx, retry.Next()) {
			return nil
		}
	}
	return nil
}

func consumeSocket(socket packetSocket, sink Sink) error {
	buffer := make([]byte, MaxDatagramSize+1)
	info := socket.Info()

	var rxqPrevious uint32
	var procPrevious uint64
	var procObserved bool
	if info.DropSource == DropProcUDP {
		procPrevious, procObserved = socket.ProcDrops()
	}

	for {
		result, err := socket.Read(buffer)
		if err != nil {
			return err
		}
		if observer, ok := sink.(interface{ DatagramReceived(int) }); ok {
			observer.DatagramReceived(result.N)
		}

		if info.DropSource == DropRXQOverflow && result.RXQOverflow != nil {
			current := *result.RXQOverflow
			delta := current - rxqPrevious
			rxqPrevious = current
			if delta != 0 {
				sink.KernelDrops(uint64(delta), DropRXQOverflow)
			}
		}
		if info.DropSource == DropProcUDP {
			if current, ok := socket.ProcDrops(); ok {
				if procObserved && current >= procPrevious && current != procPrevious {
					sink.KernelDrops(current-procPrevious, DropProcUDP)
				}
				procPrevious = current
				procObserved = true
			}
		}

		if !validLoopbackSource(result.Source) {
			sink.DatagramRejected(RejectSource)
			continue
		}
		datagram, reason := NormalizeDatagram(buffer, result.N, result.Truncated)
		if reason != RejectNone {
			sink.DatagramRejected(reason)
			continue
		}
		if err := nmea.ValidateChecksum(datagram); err != nil {
			sink.NMEARejected(true)
			continue
		}
		sentence, err := nmea.Parse(datagram, result.ReceivedAt)
		if err != nil {
			sink.NMEARejected(false)
			continue
		}
		sink.Sentence(sentence)
	}
}

func validLoopbackSource(source netip.AddrPort) bool {
	return source.IsValid() && source.Addr().Is4() && source.Addr().IsLoopback()
}

func waitForContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
