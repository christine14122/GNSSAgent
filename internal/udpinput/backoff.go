package udpinput

import "time"

var retryDelays = [...]time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	30 * time.Second,
}

type backoff struct {
	step int
}

func newBackoff() *backoff {
	return &backoff{}
}

func (b *backoff) Next() time.Duration {
	index := b.step
	if index >= len(retryDelays) {
		index = len(retryDelays) - 1
	} else {
		b.step++
	}
	return retryDelays[index]
}

func (b *backoff) Reset() {
	b.step = 0
}
