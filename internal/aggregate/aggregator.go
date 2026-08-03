package aggregate

import (
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
)

const flushDelay = 1500 * time.Millisecond

type Aggregator struct {
	current *cycle
}

func New() *Aggregator {
	return &Aggregator{}
}

func (a *Aggregator) Add(sentence nmea.Sentence) (model.FullStatus, bool) {
	second, timed := sentenceSecond(sentence)
	if a.current == nil {
		a.current = newCycle(sentence, second, timed)
		return model.FullStatus{}, false
	}

	if timed && a.current.hasSecond {
		if second != a.current.second {
			completed := a.current.status()
			a.current = newCycle(sentence, second, true)
			return completed, true
		}
	} else if timed {
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
	a.current = nil
	return completed, true
}

func (a *Aggregator) Clear() {
	a.current = nil
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
