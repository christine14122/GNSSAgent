// Package tncstime injects fresh GNSS UTC into the local TNCS time engine.
package tncstime

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"gnssagent/internal/nmea"
)

const (
	Address        = "127.0.0.1:44622"
	sendInterval   = 30 * time.Second
	silenceTimeout = 30 * time.Minute
	retryInterval  = 3 * time.Second
	ioTimeout      = 3 * time.Second
)

type request struct {
	timestamp int64
	trusted   bool
}

type Client struct {
	logger           *slog.Logger
	now              func() time.Time
	dial             func(context.Context) (net.Conn, error)
	wait             func(context.Context, time.Duration) bool
	requests         chan request
	mu               sync.Mutex
	lastReceived     time.Time
	nextSend         time.Time
	nextInvalidation time.Time
	connected        bool
	busy             bool
	invalidated      bool
}

func New(logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		logger: logger, now: time.Now, wait: wait,
		requests: make(chan request, 1),
		dial: func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{Timeout: ioTimeout}).DialContext(ctx, "tcp4", Address)
		},
	}
}

// Sentence is nonblocking. The only pending request belongs to an idle,
// connected worker; data arriving during cooldown, I/O or reconnect is discarded.
// No UTC is retained for later retries or for outage reporting.
func (c *Client) Sentence(s nmea.Sentence) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.lastReceived.IsZero() && now.Sub(c.lastReceived) >= silenceTimeout {
		c.nextSend = time.Time{}
	}
	c.lastReceived = now
	if !c.connected || c.busy || now.Before(c.nextSend) {
		return
	}
	utc, ok := sentenceUTC(s)
	if !ok {
		return
	}
	c.busy = true
	c.requests <- request{timestamp: utc, trusted: true}
}

func sentenceUTC(s nmea.Sentence) (int64, bool) {
	var date nmea.Field[time.Time]
	var millis int64
	var valid bool
	switch {
	case s.Kind == nmea.KindRMC && s.RMC != nil:
		date, millis, valid = s.RMC.Date, s.RMC.MillisOfDay, s.RMC.TimeValid
	case s.Kind == nmea.KindZDA && s.ZDA != nil:
		date, millis, valid = s.ZDA.Date, s.ZDA.MillisOfDay, s.ZDA.TimeValid
	}
	if !valid || !date.Valid || millis < 0 || millis >= 86_400_000 {
		return 0, false
	}
	utc := date.Value.UTC().Add(time.Duration(millis) * time.Millisecond).UnixMilli()
	return utc, utc > 0
}

func (c *Client) Run(ctx context.Context) {
	c.mu.Lock()
	if c.lastReceived.IsZero() {
		c.lastReceived = c.now()
	}
	c.mu.Unlock()
	conn := c.connect(ctx)
	if conn == nil {
		return
	}
	defer func() {
		if conn != nil {
			conn.Close()
		}
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var req request
		select {
		case <-ctx.Done():
			return
		case req = <-c.requests:
		case <-ticker.C:
			c.mu.Lock()
			due := !c.busy && !c.invalidated && c.now().Sub(c.lastReceived) >= silenceTimeout && !c.now().Before(c.nextInvalidation)
			if due {
				c.busy = true
			}
			c.mu.Unlock()
			if !due {
				continue
			}
			// Proto3 defaults encode zero timestamp and false trust as an empty payload.
			req = request{}
		}
		if ctx.Err() != nil {
			return
		}
		err := exchange(ctx, conn, req, func() {
			if req.trusted {
				c.mu.Lock()
				c.nextSend = c.now().Add(sendInterval)
				c.mu.Unlock()
			}
		})
		c.mu.Lock()
		if !req.trusted {
			c.nextInvalidation = c.now().Add(retryInterval)
		}
		if err == nil {
			c.invalidated = !req.trusted
			if !req.trusted {
				c.nextSend = time.Time{}
			}
		}
		_, rejected := err.(*rejection)
		if err != nil && !rejected {
			c.connected = false
		}
		c.busy = false
		c.mu.Unlock()
		if err == nil {
			c.logger.Info("TNCS time accepted", "timestamp_ms", req.timestamp, "trusted", req.trusted)
			continue
		}
		c.logger.Warn("TNCS time update failed", "trusted", req.trusted, "error", err)
		if rejected {
			continue
		}
		conn.Close()
		conn = nil
		if !c.wait(ctx, retryInterval) {
			return
		}
		conn = c.connect(ctx)
		if conn == nil {
			return
		}
	}
}

// Retry startup and runtime connections every three seconds until shutdown.
func (c *Client) connect(ctx context.Context) net.Conn {
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return nil
		}
		conn, err := c.dial(ctx)
		if err == nil {
			c.mu.Lock()
			c.connected = true
			// Preserve the send interval, and wait for a NEW sentence after reconnect.
			c.invalidated = false // A restarted TNCS may need the outage report again.
			c.mu.Unlock()
			c.logger.Info("TNCS connected", "address", Address)
			return conn
		}
		c.logger.Warn("TNCS connection failed", "attempt", attempt, "error", err)
		if !c.wait(ctx, retryInterval) {
			return nil
		}
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
