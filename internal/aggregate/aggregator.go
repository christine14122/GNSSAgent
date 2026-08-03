package aggregate

import (
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
)

const flushDelay = 1500 * time.Millisecond

type Aggregator struct {
	current            *cycle
	completedSecond    int64
	hasCompletedSecond bool
}

func New() *Aggregator {
	return &Aggregator{}
}

func (a *Aggregator) Add(sentence nmea.Sentence) (model.FullStatus, bool) {
	second, timed := sentenceSecond(sentence)
	if a.current == nil {
		if timed && a.hasCompletedSecond && !secondIsForward(a.completedSecond, second) {
			return model.FullStatus{}, false
		}
		a.current = newCycle(sentence, second, timed)
		return model.FullStatus{}, false
	}

	if timed && a.current.hasSecond {
		if second == a.current.second {
			a.current.add(sentence)
			return model.FullStatus{}, false
		}
		if !secondIsForward(a.current.second, second) {
			return model.FullStatus{}, false
		}
		completed := a.current.status()
		a.completedSecond = a.current.second
		a.hasCompletedSecond = true
		a.current = newCycle(sentence, second, true)
		return completed, true
	} else if timed {
		if a.hasCompletedSecond && !secondIsForward(a.completedSecond, second) {
			return model.FullStatus{}, false
		}
		a.current.second = second
		a.current.hasSecond = true
	}
	a.current.add(sentence)
	return model.FullStatus{}, false
}

func (a *Aggregator) FlushExpired(now time.Time) (model.FullStatus, bool) {
	if a.current == nil || now.Sub(a.current.firstReceivedAt) < flushDelay {
		return model.FullStatus{}, false
	}
	completed := a.current.status()
	if a.current.hasSecond {
		a.completedSecond = a.current.second
		a.hasCompletedSecond = true
	}
	a.current = nil
	return completed, true
}

func (a *Aggregator) Clear() {
	a.current = nil
	a.completedSecond = 0
	a.hasCompletedSecond = false
}

// secondIsForward orders UTC seconds-of-day within the half-day window around
// the previous key. Cycles flush after 1.5 seconds, so a legitimate forward
// transition cannot span twelve hours; the opposite modular direction is stale.
func secondIsForward(previous, candidate int64) bool {
	const (
		secondsPerDay = int64(24 * 60 * 60)
		halfDay       = secondsPerDay / 2
	)
	difference := (candidate - previous + secondsPerDay) % secondsPerDay
	return difference > 0 && difference < halfDay
}

func sentenceSecond(sentence nmea.Sentence) (int64, bool) {
	var millis int64
	switch sentence.Kind {
	case nmea.KindRMC:
		if sentence.RMC == nil || !sentence.RMC.TimeValid {
			return 0, false
		}
		millis = sentence.RMC.MillisOfDay
	case nmea.KindGGA:
		if sentence.GGA == nil || !sentence.GGA.TimeValid {
			return 0, false
		}
		millis = sentence.GGA.MillisOfDay
	case nmea.KindGST:
		if sentence.GST == nil || !sentence.GST.TimeValid {
			return 0, false
		}
		millis = sentence.GST.MillisOfDay
	default:
		return 0, false
	}
	if millis < 0 || millis >= int64(24*time.Hour/time.Millisecond) {
		return 0, false
	}
	return millis / 1000, true
}
