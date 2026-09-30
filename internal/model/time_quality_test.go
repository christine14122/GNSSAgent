package model

import (
	"testing"
	"time"
)

func TestTimeQualityExpiresAtDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Second)
	quality := TimeQuality{Evaluated: true, State: 2, Reason: 2, Samples: 10,
		RMSValid: true, RMS: 1, TimeoutMillis: 3000, ExpiresAt: deadline}
	if quality.Expire(deadline.Add(-time.Nanosecond)) || quality.State != 2 {
		t.Fatalf("expired early: %+v", quality)
	}
	if !quality.Expire(deadline) || quality.State != 0 || quality.Reason != 9 || quality.Samples != 0 || quality.RMSValid || quality.RMS != 0 {
		t.Fatalf("expired quality retained evidence: %+v", quality)
	}
	if quality.TimeoutMillis != 3000 || quality.ExpiresAt != deadline {
		t.Fatal("expiry changed its deadline")
	}
}

func TestTimeQualityWithoutDeadlineIsNotChanged(t *testing.T) {
	quality := TimeQuality{Evaluated: true, State: 1, Reason: 1, Samples: 1, RMSValid: true, RMS: 1}
	before := quality
	if quality.Expire(time.Now()) || quality != before {
		t.Fatal("missing internal deadline changed a synthetic status")
	}
}
