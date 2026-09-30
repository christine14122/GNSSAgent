package aggregate

import (
	"math"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
	"gnssagent/internal/timequality"
)

// utcRange retains all complete UTC evidence for one epoch, including late
// sources. The published raw UTC remains the first valid source.
type utcRange struct {
	min uint64
	max uint64
}

func (r *utcRange) include(utc uint64) {
	r.min = min(r.min, utc)
	r.max = max(r.max, utc)
}

func (r utcRange) conflicts(tolerance time.Duration) bool {
	return r.max-r.min > uint64(tolerance/time.Millisecond)
}

func (a *Aggregator) completeCycle() model.FullStatus {
	status := a.current.status()
	sample, hasGST, bounds := a.current.timeSample(status, a.qualityConfig.MaxTimeStepError)
	if sample.UTCValid && a.lastQuality.FieldValidityMask&model.FullUTCValid != 0 && sample.UTC/1000 == a.lastQuality.UTCTime/1000 {
		// A repeated epoch can be reaggregated after the short late-arrival guard
		// expires. It must not forget UTC sources already observed for that epoch.
		bounds.include(a.lastUTCRange.min)
		bounds.include(a.lastUTCRange.max)
		sample.Conflict = sample.Conflict || bounds.conflicts(a.qualityConfig.MaxTimeStepError)
	}
	result := a.quality.Update(sample)
	if a.qualityFailure != 0 {
		a.quality.Reset()
		result = timequality.Result{State: timequality.Untrusted, Reason: a.qualityFailure}
		a.qualityFailure = 0
	}
	if hasGST && !sample.RMSValid && result.Reason == timequality.NoGST {
		result.Reason = timequality.InvalidGST
	}
	status.TimeQuality = model.TimeQuality{
		Evaluated: true,
		State:     uint8(result.State), Reason: uint8(result.Reason), Samples: result.Samples,
		RMSValid: sample.RMSValid, TimeoutMillis: uint32(a.qualityConfig.Timeout / time.Millisecond),
		ExpiresAt: a.quality.ExpiresAt(),
	}
	if sample.RMSValid {
		status.TimeQuality.RMS = float32(sample.RMS)
	}
	a.lastQuality = status
	a.lastUTCRange = bounds
	return status
}

// timeSample preserves the legacy raw GST fields while selecting a conservative,
// epoch-aligned RMS for the heuristic. Invalid or undated UTC cannot confer trust.
func (c *cycle) timeSample(status model.FullStatus, tolerance time.Duration) (timequality.Sample, bool, utcRange) {
	sample := timequality.Sample{
		UTC: status.UTCTime, UTCValid: status.FieldValidityMask&model.FullUTCValid != 0,
		NavigationValid: status.FieldValidityMask&model.FullValidValid != 0 && status.Valid == 1,
		ReceivedAt:      c.firstReceivedAt,
	}
	hasGST := false
	bounds := utcRange{min: sample.UTC, max: sample.UTC}
	for _, sentence := range c.sentences {
		var date nmea.Field[time.Time]
		var millis int64
		var timed bool
		switch sentence.Kind {
		case nmea.KindRMC:
			if sentence.RMC == nil {
				continue
			}
			date, millis, timed = sentence.RMC.Date, sentence.RMC.MillisOfDay, sentence.RMC.TimeValid
		case nmea.KindZDA:
			if sentence.ZDA == nil {
				continue
			}
			date, millis, timed = sentence.ZDA.Date, sentence.ZDA.MillisOfDay, sentence.ZDA.TimeValid
		case nmea.KindGST:
			hasGST = true
			gst := sentence.GST
			if gst == nil || !gst.TimeValid || !c.hasSecond || gst.MillisOfDay < 0 || gst.MillisOfDay >= 86_400_000 || gst.MillisOfDay/1000 != c.second {
				continue
			}
			if sample.UTCValid && millisecondsApart(uint64(gst.MillisOfDay), sample.UTC%86_400_000) > uint64(tolerance/time.Millisecond) {
				continue
			}
			rms := gst.PseudorangeRMS
			if !finite64(rms) || rms.Value < 0 || rms.Value > math.MaxFloat32 {
				continue
			}
			if !sample.RMSValid || rms.Value > sample.RMS {
				sample.RMS, sample.RMSValid = rms.Value, true
			}
		}
		if sample.UTCValid && timed && date.Valid && millis >= 0 && millis < 86_400_000 {
			utc := date.Value.UTC().Add(time.Duration(millis) * time.Millisecond).UnixMilli()
			if utc < 0 {
				sample.Conflict = true
				continue
			}
			bounds.include(uint64(utc))
		}
	}
	sample.Conflict = sample.Conflict || bounds.conflicts(tolerance)
	return sample, hasGST, bounds
}

func millisecondsApart(a, b uint64) uint64 {
	if a >= b {
		return a - b
	}
	return b - a
}

// ExpireTimeQuality clears local trust after input goes stale. No navigation
// frame is emitted; consumers expire their cached status independently.
func (a *Aggregator) ExpireTimeQuality(now time.Time) (model.FullStatus, bool) {
	result, changed := a.quality.Expire(now)
	if !changed || !a.lastQuality.TimeQuality.Evaluated {
		return model.FullStatus{}, false
	}
	status := a.lastQuality
	status.TimeQuality.State = uint8(result.State)
	status.TimeQuality.Reason = uint8(result.Reason)
	status.TimeQuality.Samples = result.Samples
	status.TimeQuality.RMSValid = false
	status.TimeQuality.RMS = 0
	a.lastQuality = status
	return status, true
}

// Explicit navigation warnings revoke trust even when their epoch is duplicate
// or late. Otherwise a consistent repeat of the last published second is ignored.
func (a *Aggregator) rejectTimeQuality(sentence nmea.Sentence) {
	invalidNavigation := sentence.RMC != nil && sentence.RMC.Status.Valid && sentence.RMC.Status.Value == 'V' ||
		sentence.GGA != nil && sentence.GGA.Quality.Valid && sentence.GGA.Quality.Value == 0 ||
		sentence.GLL != nil && sentence.GLL.Status.Valid && sentence.GLL.Status.Value == 'V'
	if invalidNavigation {
		a.failTimeQuality(timequality.InvalidNavigation)
		return
	}
	var date nmea.Field[time.Time]
	var millis int64
	var valid bool
	if sentence.RMC != nil {
		date, millis, valid = sentence.RMC.Date, sentence.RMC.MillisOfDay, sentence.RMC.TimeValid
	} else if sentence.ZDA != nil {
		date, millis, valid = sentence.ZDA.Date, sentence.ZDA.MillisOfDay, sentence.ZDA.TimeValid
	}
	if !valid || !date.Valid || millis < 0 || millis >= 86_400_000 {
		return
	}
	utc := date.Value.UTC().Add(time.Duration(millis) * time.Millisecond).UnixMilli()
	if utc >= 0 && a.lastQuality.FieldValidityMask&model.FullUTCValid != 0 && uint64(utc)/1000 == a.lastQuality.UTCTime/1000 {
		a.lastUTCRange.include(uint64(utc))
		if a.lastUTCRange.conflicts(a.qualityConfig.MaxTimeStepError) {
			a.failTimeQuality(timequality.TimeConflict)
		}
		return
	}
	a.failTimeQuality(timequality.TimeJump)
}

func (a *Aggregator) failTimeQuality(reason timequality.Reason) {
	a.quality.Reset()
	a.qualityFailure = reason
	if !a.lastQuality.TimeQuality.Evaluated {
		return
	}
	a.lastQuality.TimeQuality.State = uint8(timequality.Untrusted)
	a.lastQuality.TimeQuality.Reason = uint8(reason)
	a.lastQuality.TimeQuality.Samples = 0
	a.lastQuality.TimeQuality.RMSValid = false
	a.lastQuality.TimeQuality.RMS = 0
	a.lastQuality.TimeQuality.ExpiresAt = time.Time{}
}
