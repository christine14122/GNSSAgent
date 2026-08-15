package server

import (
	"io"
	"net"
	"sync"
	"time"

	"gnssagent/internal/protocol"
)

const (
	defaultSubscriptionTimeout = 5 * time.Second
	defaultWriteTimeout        = 3 * time.Second
)

type session struct {
	conn             net.Conn
	hub              *Hub
	subscribeTimeout time.Duration
	writeTimeout     time.Duration

	stateMu    sync.RWMutex
	subscribed bool
	statusType protocol.StatusType
	subscriber *subscriber
	observer   Observer

	writeMu   sync.Mutex
	closeOnce sync.Once
	done      chan struct{}
}

func newSession(conn net.Conn, hub *Hub, subscribeTimeout, writeTimeout time.Duration) *session {
	return &session{
		conn:             conn,
		hub:              hub,
		subscribeTimeout: subscribeTimeout,
		writeTimeout:     writeTimeout,
		done:             make(chan struct{}),
	}
}

func (s *session) run() {
	defer s.close()
	if err := s.conn.SetReadDeadline(time.Now().Add(s.subscribeTimeout)); err != nil {
		return
	}

	decoder := protocol.NewDecoder(protocol.MaxPayload)
	buffer := make([]byte, 4096)
	for {
		n, err := s.conn.Read(buffer)
		if n > 0 {
			for _, frame := range decoder.Feed(buffer[:n]) {
				if !s.handleFrame(frame) {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *session) handleFrame(frame protocol.Frame) bool {
	switch frame.Type {
	case protocol.TypeSubscribeRequest:
		return s.handleSubscribe(frame)
	case protocol.TypeSwitchRequest:
		return true
	default:
		return true
	}
}

func (s *session) handleSubscribe(frame protocol.Frame) bool {
	if s.isSubscribed() {
		return s.writeACK(protocol.SubscribeAlreadySubscribed)
	}
	if frame.Version != protocol.Version {
		return s.writeACK(protocol.SubscribeUnsupportedVersion)
	}
	statusType, err := protocol.ParseSubscribeRequest(frame.Payload)
	if err != nil || (statusType != protocol.StatusSimple && statusType != protocol.StatusFull) {
		return s.writeACK(protocol.SubscribeInvalidStatusType)
	}

	subscriber := newSubscriber(statusType)
	s.stateMu.Lock()
	s.subscribed = true
	s.statusType = statusType
	s.subscriber = subscriber
	s.stateMu.Unlock()
	s.hub.add(subscriber)
	if !s.writeACK(protocol.SubscribeSuccess) {
		return false
	}
	if s.observer != nil {
		s.observer.RecordTCPSubscription()
	}
	if err := s.conn.SetReadDeadline(time.Time{}); err != nil {
		return false
	}
	go s.writeStatuses(subscriber)
	return true
}

func (s *session) writeACK(result protocol.SubscribeResult) bool {
	return s.writeFrame(protocol.EncodeSubscribeACK(result)) == nil
}

func (s *session) writeStatuses(subscriber *subscriber) {
	for {
		select {
		case frame := <-subscriber.queue:
			if err := s.writeFrame(frame); err != nil {
				s.close()
				return
			}
		case <-subscriber.done:
			return
		}
	}
}

func (s *session) writeFrame(frame []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
		return err
	}
	for len(frame) > 0 {
		n, err := s.conn.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		frame = frame[n:]
	}
	return nil
}

func (s *session) isSubscribed() bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.subscribed
}

func (s *session) selectedStatusType() protocol.StatusType {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.statusType
}

func (s *session) close() {
	s.closeOnce.Do(func() {
		s.stateMu.RLock()
		subscriber := s.subscriber
		s.stateMu.RUnlock()
		if subscriber != nil {
			s.hub.remove(subscriber)
		}
		_ = s.conn.Close()
		close(s.done)
	})
}
