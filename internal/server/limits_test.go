package server

import "testing"

func TestLimiterReservesOneLoopbackSlot(t *testing.T) {
	limiter := newLimiter(5, 4)
	var releases []func()
	for i := 0; i < 4; i++ {
		if release, ok := limiter.acquire(false); !ok {
			t.Fatalf("remote %d rejected", i)
		} else {
			releases = append(releases, release)
		}
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()

	if _, ok := limiter.acquire(false); ok {
		t.Fatal("fifth remote must be rejected")
	}
	if release, ok := limiter.acquire(true); !ok {
		t.Fatal("loopback fifth connection must be accepted")
	} else {
		release()
	}
}

func TestLimiterEnforcesTotalAndIdempotentRelease(t *testing.T) {
	limiter := newLimiter(2, 1)
	loopbackRelease, ok := limiter.acquire(true)
	if !ok {
		t.Fatal("first loopback rejected")
	}
	remoteRelease, ok := limiter.acquire(false)
	if !ok {
		t.Fatal("remote rejected")
	}
	if _, ok := limiter.acquire(true); ok {
		t.Fatal("connection beyond total limit accepted")
	}

	remoteRelease()
	remoteRelease()
	if replacementRelease, ok := limiter.acquire(false); !ok {
		t.Fatal("idempotent release did not free exactly one remote slot")
	} else {
		replacementRelease()
	}
	loopbackRelease()
	loopbackRelease()
}
