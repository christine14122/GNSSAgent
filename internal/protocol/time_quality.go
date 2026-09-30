package protocol

import "gnssagent/internal/model"

// sanitizeTimeQuality applies identical trust prerequisites to SIMPLE and FULL.
func sanitizeTimeQuality(quality model.TimeQuality, utcValid, recvValid bool) model.TimeQuality {
	if !quality.Evaluated {
		return model.TimeQuality{}
	}
	// Unknown enum values must not retain a trusted state or convergence count.
	if quality.State > 3 || quality.Reason > 10 {
		quality.State, quality.Reason, quality.Samples = 0, 0, 0
	}
	if !quality.RMSValid || !nonnegative32(quality.RMS) {
		quality.RMSValid, quality.RMS = false, 0
	}
	if quality.State == 2 { // Trusted requires usable time, RMS, and a freshness limit.
		switch {
		case !utcValid || !recvValid:
			quality.State, quality.Reason, quality.Samples = 0, 5, 0 // Unknown / InvalidUTC
		case !quality.RMSValid:
			quality.State, quality.Reason, quality.Samples = 0, 10, 0 // Unknown / InvalidGST
		case quality.TimeoutMillis == 0:
			quality.State, quality.Reason, quality.Samples = 0, 9, 0 // Unknown / Stale
		}
	}
	return quality
}
