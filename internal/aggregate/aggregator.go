package aggregate

import (
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
	"gnssagent/internal/timequality"
)

const flushDelay = 1500 * time.Millisecond

// lateArrivalWindow bounds duplicate/stale suppression to two flush periods.
// Beyond it, receive-time continuity is no longer assumed and a new stream can
// rebase its seconds-of-day ordering guard.
const lateArrivalWindow = 2 * flushDelay

type Aggregator struct {
	current            *cycle
	completedSecond    int64
	hasCompletedSecond bool
	completedAt        time.Time
	quality            *timequality.Tracker
	qualityConfig      timequality.Config
	lastQuality        model.FullStatus
	lastUTCRange       utcRange
	qualityFailure     timequality.Reason
}

func New(options ...timequality.Config) *Aggregator {
	cfg := timequality.DefaultConfig()
	if len(options) != 0 {
		cfg = options[0]
	}
	return &Aggregator{quality: timequality.New(cfg), qualityConfig: cfg}
}

func (a *Aggregator) Add(sentence nmea.Sentence) (model.FullStatus, bool) {
	second, timed := sentenceSecond(sentence)
	if timed && (a.current == nil || !a.current.hasSecond) {
		a.expireCompletedGuard(sentence.ReceivedAt)
	}
	if a.current == nil {
		if timed && a.hasCompletedSecond && !secondIsForward(a.completedSecond, second) {
			a.rejectTimeQuality(sentence)
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
			a.rejectTimeQuality(sentence)
			return model.FullStatus{}, false
		}
		completed := a.completeCycle()
		a.setCompletedGuard(a.current.second, sentence.ReceivedAt)
		a.current = newCycle(sentence, second, true)
		return completed, true
	} else if timed {
		if a.hasCompletedSecond && !secondIsForward(a.completedSecond, second) {
			a.rejectTimeQuality(sentence)
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
	completed := a.completeCycle()
	if a.current.hasSecond {
		a.setCompletedGuard(a.current.second, now)
	}
	a.current = nil
	return completed, true
}

func (a *Aggregator) Clear() {
	a.current = nil
	a.completedSecond = 0
	a.hasCompletedSecond = false
	a.completedAt = time.Time{}
	a.quality.Reset()
	a.lastQuality = model.FullStatus{}
	a.lastUTCRange = utcRange{}
	a.qualityFailure = 0
}

func (a *Aggregator) setCompletedGuard(second int64, completedAt time.Time) {
	a.completedSecond = second
	a.hasCompletedSecond = true
	a.completedAt = completedAt
}

func (a *Aggregator) expireCompletedGuard(reference time.Time) {
	if !a.hasCompletedSecond {
		return
	}
	elapsed := reference.Sub(a.completedAt)
	if elapsed < lateArrivalWindow && elapsed > -lateArrivalWindow {
		return
	}
	a.completedSecond = 0
	a.hasCompletedSecond = false
	a.completedAt = time.Time{}
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
	case nmea.KindZDA:
		if sentence.ZDA == nil || !sentence.ZDA.TimeValid {
			return 0, false
		}
		millis = sentence.ZDA.MillisOfDay
	case nmea.KindGLL:
		if sentence.GLL == nil || !sentence.GLL.TimeValid {
			return 0, false
		}
		millis = sentence.GLL.MillisOfDay
	default:
		return 0, false
	}
	if millis < 0 || millis >= int64(24*time.Hour/time.Millisecond) {
		return 0, false
	}
	return millis / 1000, true
}
