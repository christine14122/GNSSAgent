package udpinput

import (
	"testing"
	"time"
)

func TestBackoffSequenceAndReset(t *testing.T) {
	b := newBackoff()
	want := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for i, duration := range want {
		if got := b.Next(); got != duration {
			t.Fatalf("step %d got %v want %v", i, got, duration)
		}
	}
	b.Reset()
	if got := b.Next(); got != time.Second {
		t.Fatalf("after reset got %v", got)
	}
}
