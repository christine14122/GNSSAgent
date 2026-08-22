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
	DatagramReceived(bytes int)
	DatagramRejected(RejectReason)
	NMEARejected(checksum bool)
	KernelDrops(delta uint64, source DropSource)
	InputEvent(InputEvent)
}

type InputEventKind uint8

const (
	InputBindFailed InputEventKind = iota + 1
	InputReadFailed
	InputSocketReady
	InputSocketClosed
)

type InputEvent struct {
	Kind       InputEventKind
	Err        error
	RetryIn    time.Duration
	Attempt    uint32
	SocketInfo SocketInfo
	Recovered  bool
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
	recovering := false
	var attempt uint32
	for ctx.Err() == nil {
		socket, err := m.factory.Listen(m.address)
		if err != nil {
			delay := retry.Next()
			attempt++
			sink.InputEvent(InputEvent{Kind: InputBindFailed, Err: err, RetryIn: delay, Attempt: attempt})
			recovering = true
			if !m.sleep(ctx, delay) {
				return nil
			}
			continue
		}

		if ctx.Err() != nil {
			_ = socket.Close()
			return nil
		}
		retry.Reset()
		sink.InputEvent(InputEvent{Kind: InputSocketReady, SocketInfo: socket.Info(), Recovered: recovering})
		recovering = false
		attempt = 0

		stopClose := context.AfterFunc(ctx, func() {
			_ = socket.Close()
		})
		err = consumeSocket(socket, sink)
		stopClose()
		_ = socket.Close()
		if ctx.Err() != nil {
			sink.InputEvent(InputEvent{Kind: InputSocketClosed})
			return nil
		}
		if err != nil {
			sink.Reset()
			delay := retry.Next()
			attempt++
			sink.InputEvent(InputEvent{Kind: InputReadFailed, Err: err, RetryIn: delay, Attempt: attempt})
			recovering = true
			if !m.sleep(ctx, delay) {
				return nil
			}
			continue
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
		sink.DatagramReceived(result.N)

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

		if !validIPv4Source(result.Source) {
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

func validIPv4Source(source netip.AddrPort) bool {
	return source.IsValid() && source.Addr().Is4()
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
