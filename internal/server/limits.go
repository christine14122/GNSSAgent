package server

import "sync"

type limiter struct {
	mu        sync.Mutex
	maxTotal  int
	maxRemote int
	total     int
	remote    int
}

func newLimiter(maxTotal, maxRemote int) *limiter {
	return &limiter{maxTotal: maxTotal, maxRemote: maxRemote}
}

func (l *limiter) acquire(loopback bool) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.maxTotal || (!loopback && l.remote >= l.maxRemote) {
		return nil, false
	}
	l.total++
	if !loopback {
		l.remote++
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.total--
			if !loopback {
				l.remote--
			}
		})
	}, true
}
