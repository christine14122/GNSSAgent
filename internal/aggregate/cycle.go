package aggregate

import (
	"math"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
)

type cycle struct {
	second          int64
	hasSecond       bool
	firstReceivedAt time.Time
	sentences       []nmea.Sentence
}

func newCycle(sentence nmea.Sentence, second int64, timed bool) *cycle {
	return &cycle{
		second:          second,
		hasSecond:       timed,
		firstReceivedAt: sentence.ReceivedAt,
		sentences:       []nmea.Sentence{cloneSentence(sentence)},
	}
}

func (c *cycle) add(sentence nmea.Sentence) {
	c.sentences = append(c.sentences, cloneSentence(sentence))
}

func cloneSentence(sentence nmea.Sentence) nmea.Sentence {
	clone := sentence
	if sentence.RMC != nil {
		payload := *sentence.RMC
		clone.RMC = &payload
	}
	if sentence.GGA != nil {
		payload := *sentence.GGA
		clone.GGA = &payload
	}
	if sentence.GSA != nil {
		payload := *sentence.GSA
		payload.PRNs = append([]string(nil), sentence.GSA.PRNs...)
		clone.GSA = &payload
	}
	if sentence.GSV != nil {
		payload := *sentence.GSV
		payload.Satellites = append([]nmea.Satellite(nil), sentence.GSV.Satellites...)
		clone.GSV = &payload
	}
	if sentence.GST != nil {
		payload := *sentence.GST
		clone.GST = &payload
	}
	if sentence.ZDA != nil {
		payload := *sentence.ZDA
		clone.ZDA = &payload
	}
	if sentence.GLL != nil {
		payload := *sentence.GLL
		clone.GLL = &payload
	}
	return clone
}

func (c *cycle) status() model.FullStatus {
	var status model.FullStatus
	setReceiveTime(&status, c.firstReceivedAt)

	var ggaLatitude, ggaLongitude, rmcLatitude, rmcLongitude, gllLatitude, gllLongitude nmea.Field[float64]
	var ggaUsed nmea.Field[uint8]
	var explicitTrue, explicitFalse bool
	var dopGroups []dopGroup
	fixDimension := uint8(0)
	fixDimensionValid := false

	for _, sentence := range c.sentences {
		switch sentence.Kind {
		case nmea.KindRMC:
			rmc := sentence.RMC
			if rmc == nil {
				continue
			}
			setUTC(&status, rmc)
			rememberFinite64(&rmcLatitude, rmc.Latitude)
			rememberFinite64(&rmcLongitude, rmc.Longitude)
			setRMCFields(&status, rmc)
			if rmc.Status.Valid {
				switch rmc.Status.Value {
				case 'A':
					explicitTrue = true
				case 'V':
					explicitFalse = true
				}
			}
		case nmea.KindGGA:
			gga := sentence.GGA
			if gga == nil {
				continue
			}
			rememberFinite64(&ggaLatitude, gga.Latitude)
			rememberFinite64(&ggaLongitude, gga.Longitude)
			setGGAFields(&status, gga)
			if !ggaUsed.Valid && gga.UsedSatellites.Valid {
				ggaUsed = gga.UsedSatellites
			}
			if gga.Quality.Valid {
				if gga.Quality.Value > 0 {
					explicitTrue = true
				} else {
					explicitFalse = true
				}
			}
		case nmea.KindGSA:
			gsa := sentence.GSA
			if gsa == nil {
				continue
			}
			if gsa.FixDimension.Valid && (!fixDimensionValid || gsa.FixDimension.Value > fixDimension) {
				fixDimension = gsa.FixDimension.Value
				fixDimensionValid = true
			}
			if group, usable := usableGSAGroup(gsa); usable {
				dopGroups = append(dopGroups, group)
			}
		case nmea.KindGST:
			if sentence.GST != nil {
				setGSTFields(&status, sentence.GST)
			}
		case nmea.KindZDA:
			if sentence.ZDA != nil {
				setUTCFromDateAndTime(&status, sentence.ZDA.Date, sentence.ZDA.MillisOfDay, sentence.ZDA.TimeValid)
			}
		case nmea.KindGLL:
			gll := sentence.GLL
			if gll == nil {
				continue
			}
			rememberFinite64(&gllLatitude, gll.Latitude)
			rememberFinite64(&gllLongitude, gll.Longitude)
			if gll.Status.Valid {
				switch gll.Status.Value {
				case 'A':
					explicitTrue = true
				case 'V':
					explicitFalse = true
				}
			}
		}
	}

	setPreferredPosition(&status, ggaLatitude, ggaLongitude, rmcLatitude, rmcLongitude, gllLatitude, gllLongitude)
	if explicitTrue || explicitFalse {
		status.FieldValidityMask |= model.FullValidValid
		if explicitTrue && !explicitFalse {
			status.Valid = 1
		}
	}
	if fixDimensionValid {
		status.FixDimension = fixDimension
		status.FieldValidityMask |= model.FullFixDimensionValid
	}
	if group, valid := reconcileGSADOP(dopGroups); valid {
		status.GSAPDOP = group.pdop.value
		status.GSAHDOP = group.hdop.value
		status.GSAVDOP = group.vdop.value
		status.FieldValidityMask |= model.FullGSAPDOPValid | model.FullGSAHDOPValid | model.FullGSAVDOPValid
	}

	complete := collectCompleteGSVs(c.sentences)
	setConstellationCounts(&status, complete)
	identities := gsaIdentities(c.sentences)
	if ggaUsed.Valid {
		status.UsedSatellites = ggaUsed.Value
		status.FieldValidityMask |= model.FullUsedSatellitesValid
	} else if count := countUsedSatellites(identities, complete); count.Valid {
		status.UsedSatellites = count.Value
		status.FieldValidityMask |= model.FullUsedSatellitesValid
	}
	if average, valid := averageUsedCN0(identities, complete); valid {
		status.AvgUsedCN0 = average
		status.FieldValidityMask |= model.FullAvgUsedCN0Valid
	}
	return status
}

func setReceiveTime(status *model.FullStatus, receivedAt time.Time) {
	millis := receivedAt.UnixMilli()
	if millis < 0 {
		return
	}
	status.RecvTime = uint64(millis)
	status.FieldValidityMask |= model.FullRecvValid
}

func setUTC(status *model.FullStatus, rmc *nmea.RMC) {
	setUTCFromDateAndTime(status, rmc.Date, rmc.MillisOfDay, rmc.TimeValid)
}

func setUTCFromDateAndTime(status *model.FullStatus, date nmea.Field[time.Time], millisOfDay int64, timeValid bool) {
	if status.FieldValidityMask&model.FullUTCValid != 0 || !timeValid || !date.Valid || millisOfDay < 0 || millisOfDay >= int64(24*time.Hour/time.Millisecond) {
		return
	}
	millis := date.Value.UTC().Add(time.Duration(millisOfDay) * time.Millisecond).UnixMilli()
	if millis < 0 {
		return
	}
	status.UTCTime = uint64(millis)
	status.FieldValidityMask |= model.FullUTCValid
}

func setRMCFields(status *model.FullStatus, rmc *nmea.RMC) {
	if status.FieldValidityMask&model.FullGroundSpeedValid == 0 && finite64(rmc.SpeedKnots) && rmc.SpeedKnots.Value >= 0 {
		value := rmc.SpeedKnots.Value * 0.5144444444444445
		if value <= math.MaxFloat32 {
			status.GroundSpeedMPS = float32(value)
			status.FieldValidityMask |= model.FullGroundSpeedValid
		}
	}
	if status.FieldValidityMask&model.FullCourseValid == 0 && finite64(rmc.CourseDeg) {
		value := rmc.CourseDeg.Value
		if value >= 0 && value < 360 && value <= math.MaxFloat32 {
			status.CourseOverGroundDeg = float32(value)
			status.FieldValidityMask |= model.FullCourseValid
		}
	}
}

func setGGAFields(status *model.FullStatus, gga *nmea.GGA) {
	if status.FieldValidityMask&model.FullAltitudeMSLValid == 0 && finite64(gga.AltitudeMSL) {
		status.AltitudeMSL = gga.AltitudeMSL.Value
		status.FieldValidityMask |= model.FullAltitudeMSLValid
	}
	if status.FieldValidityMask&model.FullAltitudeEllipsoidValid == 0 && gga.Quality.Valid && gga.Quality.Value > 0 && finite64(gga.AltitudeMSL) && finite64(gga.GeoidSeparation) {
		ellipsoid := gga.AltitudeMSL.Value + gga.GeoidSeparation.Value
		if !math.IsNaN(ellipsoid) && !math.IsInf(ellipsoid, 0) {
			status.AltitudeEllipsoid = ellipsoid
			status.FieldValidityMask |= model.FullAltitudeEllipsoidValid
		}
	}
	if status.FieldValidityMask&model.FullSolutionTypeValid == 0 && gga.Quality.Valid {
		status.SolutionType = gga.Quality.Value
		status.FieldValidityMask |= model.FullSolutionTypeValid
	}
	if status.FieldValidityMask&model.FullGGAHDOPValid == 0 && gga.Quality.Valid && gga.Quality.Value > 0 {
		if hdop, valid := parseDOP(gga.HDOPText); valid {
			status.GGAHDOP = hdop.value
			status.FieldValidityMask |= model.FullGGAHDOPValid
		}
	}
	if status.FieldValidityMask&model.FullDifferentialAgeValid == 0 && finite64(gga.DifferentialAge) && gga.DifferentialAge.Value >= 0 && gga.DifferentialAge.Value <= math.MaxFloat32 {
		status.DifferentialAge = float32(gga.DifferentialAge.Value)
		status.FieldValidityMask |= model.FullDifferentialAgeValid
	}
}

func setGSTFields(status *model.FullStatus, gst *nmea.GST) {
	setFloat32Field(status, model.FullGSTPseudorangeRMSValid, &status.GSTPseudorangeRMS, gst.PseudorangeRMS)
	setFloat32Field(status, model.FullGSTSemiMajorValid, &status.GSTSemiMajorError, gst.SemiMajorError)
	setFloat32Field(status, model.FullGSTSemiMinorValid, &status.GSTSemiMinorError, gst.SemiMinorError)
	setFloat32Field(status, model.FullGSTOrientationValid, &status.GSTOrientationDeg, gst.OrientationDeg)
	setFloat32Field(status, model.FullGSTLatitudeErrorValid, &status.GSTLatitudeError, gst.LatitudeError)
	setFloat32Field(status, model.FullGSTLongitudeErrorValid, &status.GSTLongitudeError, gst.LongitudeError)
	setFloat32Field(status, model.FullGSTAltitudeErrorValid, &status.GSTAltitudeError, gst.AltitudeError)
}

func setFloat32Field(status *model.FullStatus, bit uint64, destination *float32, source nmea.Field[float64]) {
	if status.FieldValidityMask&bit != 0 || !finite64(source) || source.Value < 0 || source.Value > math.MaxFloat32 {
		return
	}
	*destination = float32(source.Value)
	status.FieldValidityMask |= bit
}

func rememberFinite64(destination *nmea.Field[float64], source nmea.Field[float64]) {
	if !destination.Valid && finite64(source) {
		*destination = source
	}
}

func finite64(field nmea.Field[float64]) bool {
	return field.Valid && !math.IsNaN(field.Value) && !math.IsInf(field.Value, 0)
}

func setPreferredPosition(status *model.FullStatus, ggaLatitude, ggaLongitude, rmcLatitude, rmcLongitude, gllLatitude, gllLongitude nmea.Field[float64]) {
	latitude := ggaLatitude
	if !latitude.Valid {
		latitude = rmcLatitude
	}
	if !latitude.Valid {
		latitude = gllLatitude
	}
	longitude := ggaLongitude
	if !longitude.Valid {
		longitude = rmcLongitude
	}
	if !longitude.Valid {
		longitude = gllLongitude
	}
	if latitude.Valid {
		status.Latitude = latitude.Value
		status.FieldValidityMask |= model.FullLatitudeValid
	}
	if longitude.Valid {
		status.Longitude = longitude.Value
		status.FieldValidityMask |= model.FullLongitudeValid
	}
}

func setConstellationCounts(status *model.FullStatus, complete map[string]completeGSV) {
	if set, valid := complete[constellationGPS]; valid {
		status.GPSSatellites = set.visible
		status.FieldValidityMask |= model.FullGPSSatellitesValid
	}
	if set, valid := complete[constellationBeiDou]; valid {
		status.BeiDouSatellites = set.visible
		status.FieldValidityMask |= model.FullBeiDouSatellitesValid
	}
	if set, valid := complete[constellationGLONASS]; valid {
		status.GLONASSSatellites = set.visible
		status.FieldValidityMask |= model.FullGLONASSSatellitesValid
	}
	if set, valid := complete[constellationGalileo]; valid {
		status.GalileoSatellites = set.visible
		status.FieldValidityMask |= model.FullGalileoSatellitesValid
	}
}
