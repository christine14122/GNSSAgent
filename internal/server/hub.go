package server

import (
	"sync"

	"gnssagent/internal/model"
	"gnssagent/internal/protocol"
)

type subscriber struct {
	statusType protocol.StatusType
	queue      chan []byte
	done       chan struct{}
	closeOnce  sync.Once
}

func newSubscriber(statusType protocol.StatusType) *subscriber {
	return &subscriber{
		statusType: statusType,
		queue:      make(chan []byte, 1),
		done:       make(chan struct{}),
	}
}

func (s *subscriber) offer(frame []byte) {
	select {
	case <-s.done:
		return
	default:
	}

	select {
	case s.queue <- frame:
		return
	default:
	}
	select {
	case <-s.queue:
	default:
	}
	select {
	case <-s.done:
	case s.queue <- frame:
	default:
	}
}

func (s *subscriber) close() {
	s.closeOnce.Do(func() { close(s.done) })
}

type Hub struct {
	mu          sync.RWMutex
	subscribers map[*subscriber]struct{}
}

func NewHub() *Hub {
	return &Hub{subscribers: make(map[*subscriber]struct{})}
}

func (h *Hub) add(subscriber *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subscribers[subscriber] = struct{}{}
}

func (h *Hub) remove(subscriber *subscriber) {
	h.mu.Lock()
	delete(h.subscribers, subscriber)
	h.mu.Unlock()
	subscriber.close()
}

func (h *Hub) Publish(status model.FullStatus) {
	h.mu.RLock()
	subscribers := make([]*subscriber, 0, len(h.subscribers))
	for subscriber := range h.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	h.mu.RUnlock()
	if len(subscribers) == 0 {
		return
	}

	var simpleFrame []byte
	var fullFrame []byte
	for _, subscriber := range subscribers {
		switch subscriber.statusType {
		case protocol.StatusSimple:
			if simpleFrame == nil {
				simpleFrame = protocol.EncodeSimple(status.Simple())
			}
			subscriber.offer(simpleFrame)
		case protocol.StatusFull:
			if fullFrame == nil {
				fullFrame = protocol.EncodeFull(status)
			}
			subscriber.offer(fullFrame)
		}
	}
}
