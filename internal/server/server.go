package server

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/protocol"
)

const rejectionWriteTimeout = 100 * time.Millisecond

type listenFunc func(network, address string) (net.Listener, error)
type tryReadFunc func(net.Conn, []byte) (int, bool)

type Server struct {
	address string
	limiter *limiter
	hub     *Hub
	listen  listenFunc
	tryRead tryReadFunc

	sessionsMu sync.Mutex
	sessions   map[*session]struct{}
	wg         sync.WaitGroup
}

func New(address string, maxConnections, maxRemoteConnections int) *Server {
	return newServer(address, maxConnections, maxRemoteConnections, net.Listen, tryReadBuffered)
}

func newServer(address string, maxConnections, maxRemoteConnections int, listen listenFunc, tryRead tryReadFunc) *Server {
	return &Server{
		address:  address,
		limiter:  newLimiter(maxConnections, maxRemoteConnections),
		hub:      NewHub(),
		listen:   listen,
		tryRead:  tryRead,
		sessions: make(map[*session]struct{}),
	}
}

func (s *Server) Publish(status model.FullStatus) {
	s.hub.Publish(status)
}

func (s *Server) Run(ctx context.Context) error {
	listener, err := s.listen("tcp", s.address)
	if err != nil {
		return err
	}
	stopClose := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer func() {
		stopClose()
		_ = listener.Close()
		s.closeSessions()
		s.wg.Wait()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}

		release, ok := s.limiter.acquire(isLoopbackConnection(conn))
		if !ok {
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.rejectFull(conn)
			}()
			continue
		}

		session := newSession(conn, s.hub, defaultSubscriptionTimeout, defaultWriteTimeout)
		s.sessionsMu.Lock()
		s.sessions[session] = struct{}{}
		s.sessionsMu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer release()
			session.run()
			s.sessionsMu.Lock()
			delete(s.sessions, session)
			s.sessionsMu.Unlock()
		}()
	}
}

func (s *Server) rejectFull(conn net.Conn) {
	defer conn.Close()
	buffer := make([]byte, protocol.HeaderSize+protocol.MaxPayload)
	n, ok := s.tryRead(conn, buffer)
	if !ok || n <= 0 || n > len(buffer) {
		return
	}
	decoder := protocol.NewDecoder(protocol.MaxPayload)
	for _, frame := range decoder.Feed(buffer[:n]) {
		if frame.Version != protocol.Version || frame.Type != protocol.TypeSubscribeRequest {
			continue
		}
		statusType, err := protocol.ParseSubscribeRequest(frame.Payload)
		if err != nil || (statusType != protocol.StatusSimple && statusType != protocol.StatusFull) {
			continue
		}
		_ = conn.SetWriteDeadline(time.Now().Add(rejectionWriteTimeout))
		_ = writeConnAll(conn, protocol.EncodeSubscribeACK(protocol.SubscribeServerFull))
		return
	}
}

func (s *Server) closeSessions() {
	s.sessionsMu.Lock()
	sessions := make([]*session, 0, len(s.sessions))
	for session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.sessionsMu.Unlock()
	for _, session := range sessions {
		session.close()
	}
}

func isLoopbackConnection(conn net.Conn) bool {
	if address, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		return address.IP.IsLoopback()
	}
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return false
	}
	return net.ParseIP(host).IsLoopback()
}

func writeConnAll(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("connection write made no progress")
		}
		data = data[n:]
	}
	return nil
}
