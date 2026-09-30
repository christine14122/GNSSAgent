package protocol

import (
	"encoding/binary"
	"math"

	"gnssagent/internal/model"
)

const (
	simplePayloadSize = 64
	fullPayloadSize   = 136
	simpleMask        = uint64(1<<9) - 1
	fullMask          = uint64(1<<30) - 1
)

func EncodeSimple(status model.SimpleStatus) []byte {
	mask := status.FieldValidityMask & simpleMask

	zeroUint64IfOff(mask, model.SimpleUTCValid, &status.UTCTime)
	zeroUint64IfOff(mask, model.SimpleRecvValid, &status.RecvTime)
	sanitizeFloat64(&mask, model.SimpleLatitudeValid, &status.Latitude, validLatitude)
	sanitizeFloat64(&mask, model.SimpleLongitudeValid, &status.Longitude, validLongitude)
	sanitizeFloat64(&mask, model.SimpleAltitudeMSLValid, &status.AltitudeMSL, finite64)
	sanitizeFloat32(&mask, model.SimpleGroundSpeedValid, &status.GroundSpeedMPS, nonnegative32)
	sanitizeFloat32(&mask, model.SimpleCourseValid, &status.CourseOverGroundDeg, angle32)
	sanitizeValid(&mask, model.SimpleValidValid, &status.Valid)
	zeroUint8IfOff(mask, model.SimpleUsedSatellitesValid, &status.UsedSatellites)
	quality := sanitizeTimeQuality(status.TimeQuality, mask&model.SimpleUTCValid != 0, mask&model.SimpleRecvValid != 0)

	payload := make([]byte, simplePayloadSize)
	binary.BigEndian.PutUint64(payload[0:8], mask)
	binary.BigEndian.PutUint64(payload[8:16], status.UTCTime)
	binary.BigEndian.PutUint64(payload[16:24], status.RecvTime)
	binary.BigEndian.PutUint64(payload[24:32], math.Float64bits(status.Latitude))
	binary.BigEndian.PutUint64(payload[32:40], math.Float64bits(status.Longitude))
	binary.BigEndian.PutUint64(payload[40:48], math.Float64bits(status.AltitudeMSL))
	binary.BigEndian.PutUint32(payload[48:52], math.Float32bits(status.GroundSpeedMPS))
	binary.BigEndian.PutUint32(payload[52:56], math.Float32bits(status.CourseOverGroundDeg))
	payload[56] = status.Valid
	payload[57] = status.UsedSatellites
	payload[58] = quality.State
	payload[59] = quality.Reason
	binary.BigEndian.PutUint32(payload[60:64], quality.TimeoutMillis)
	return frame(TypeStatusSimple, payload)
}

func EncodeFull(status model.FullStatus) []byte {
	mask := status.FieldValidityMask & fullMask &^ model.FullTimeRMSValid

	zeroUint64IfOff(mask, model.FullUTCValid, &status.UTCTime)
	zeroUint64IfOff(mask, model.FullRecvValid, &status.RecvTime)
	sanitizeFloat64(&mask, model.FullLatitudeValid, &status.Latitude, validLatitude)
	sanitizeFloat64(&mask, model.FullLongitudeValid, &status.Longitude, validLongitude)
	sanitizeFloat64(&mask, model.FullAltitudeMSLValid, &status.AltitudeMSL, finite64)
	sanitizeFloat64(&mask, model.FullAltitudeEllipsoidValid, &status.AltitudeEllipsoid, finite64)
	sanitizeValid(&mask, model.FullValidValid, &status.Valid)
	sanitizeFixDimension(&mask, model.FullFixDimensionValid, &status.FixDimension)
	zeroUint8IfOff(mask, model.FullSolutionTypeValid, &status.SolutionType)
	zeroUint8IfOff(mask, model.FullUsedSatellitesValid, &status.UsedSatellites)
	zeroUint8IfOff(mask, model.FullGPSSatellitesValid, &status.GPSSatellites)
	zeroUint8IfOff(mask, model.FullBeiDouSatellitesValid, &status.BeiDouSatellites)
	zeroUint8IfOff(mask, model.FullGLONASSSatellitesValid, &status.GLONASSSatellites)
	zeroUint8IfOff(mask, model.FullGalileoSatellitesValid, &status.GalileoSatellites)
	sanitizeFloat32(&mask, model.FullGGAHDOPValid, &status.GGAHDOP, nonnegative32)
	sanitizeFloat32(&mask, model.FullGSAPDOPValid, &status.GSAPDOP, nonnegative32)
	sanitizeFloat32(&mask, model.FullGSAHDOPValid, &status.GSAHDOP, nonnegative32)
	sanitizeFloat32(&mask, model.FullGSAVDOPValid, &status.GSAVDOP, nonnegative32)
	sanitizeFloat32(&mask, model.FullDifferentialAgeValid, &status.DifferentialAge, nonnegative32)
	sanitizeFloat32(&mask, model.FullAvgUsedCN0Valid, &status.AvgUsedCN0, nonnegative32)
	sanitizeFloat32(&mask, model.FullGroundSpeedValid, &status.GroundSpeedMPS, nonnegative32)
	sanitizeFloat32(&mask, model.FullCourseValid, &status.CourseOverGroundDeg, angle32)
	sanitizeFloat32(&mask, model.FullGSTPseudorangeRMSValid, &status.GSTPseudorangeRMS, nonnegative32)
	sanitizeFloat32(&mask, model.FullGSTSemiMajorValid, &status.GSTSemiMajorError, nonnegative32)
	sanitizeFloat32(&mask, model.FullGSTSemiMinorValid, &status.GSTSemiMinorError, nonnegative32)
	sanitizeFloat32(&mask, model.FullGSTOrientationValid, &status.GSTOrientationDeg, angle32)
	sanitizeFloat32(&mask, model.FullGSTLatitudeErrorValid, &status.GSTLatitudeError, nonnegative32)
	sanitizeFloat32(&mask, model.FullGSTLongitudeErrorValid, &status.GSTLongitudeError, nonnegative32)
	sanitizeFloat32(&mask, model.FullGSTAltitudeErrorValid, &status.GSTAltitudeError, nonnegative32)
	quality := sanitizeTimeQuality(status.TimeQuality, mask&model.FullUTCValid != 0, mask&model.FullRecvValid != 0)
	if quality.RMSValid {
		mask |= model.FullTimeRMSValid
	}

	payload := make([]byte, fullPayloadSize)
	binary.BigEndian.PutUint64(payload[0:8], mask)
	binary.BigEndian.PutUint64(payload[8:16], status.UTCTime)
	binary.BigEndian.PutUint64(payload[16:24], status.RecvTime)
	binary.BigEndian.PutUint64(payload[24:32], math.Float64bits(status.Latitude))
	binary.BigEndian.PutUint64(payload[32:40], math.Float64bits(status.Longitude))
	binary.BigEndian.PutUint64(payload[40:48], math.Float64bits(status.AltitudeMSL))
	binary.BigEndian.PutUint64(payload[48:56], math.Float64bits(status.AltitudeEllipsoid))
	payload[56] = status.Valid
	payload[57] = status.FixDimension
	payload[58] = status.SolutionType
	payload[59] = status.UsedSatellites
	payload[60] = status.GPSSatellites
	payload[61] = status.BeiDouSatellites
	payload[62] = status.GLONASSSatellites
	payload[63] = status.GalileoSatellites
	binary.BigEndian.PutUint32(payload[64:68], math.Float32bits(status.GGAHDOP))
	binary.BigEndian.PutUint32(payload[68:72], math.Float32bits(status.GSAPDOP))
	binary.BigEndian.PutUint32(payload[72:76], math.Float32bits(status.GSAHDOP))
	binary.BigEndian.PutUint32(payload[76:80], math.Float32bits(status.GSAVDOP))
	binary.BigEndian.PutUint32(payload[80:84], math.Float32bits(status.DifferentialAge))
	binary.BigEndian.PutUint32(payload[84:88], math.Float32bits(status.AvgUsedCN0))
	binary.BigEndian.PutUint32(payload[88:92], math.Float32bits(status.GroundSpeedMPS))
	binary.BigEndian.PutUint32(payload[92:96], math.Float32bits(status.CourseOverGroundDeg))
	binary.BigEndian.PutUint32(payload[96:100], math.Float32bits(status.GSTPseudorangeRMS))
	binary.BigEndian.PutUint32(payload[100:104], math.Float32bits(status.GSTSemiMajorError))
	binary.BigEndian.PutUint32(payload[104:108], math.Float32bits(status.GSTSemiMinorError))
	binary.BigEndian.PutUint32(payload[108:112], math.Float32bits(status.GSTOrientationDeg))
	binary.BigEndian.PutUint32(payload[112:116], math.Float32bits(status.GSTLatitudeError))
	binary.BigEndian.PutUint32(payload[116:120], math.Float32bits(status.GSTLongitudeError))
	binary.BigEndian.PutUint32(payload[120:124], math.Float32bits(status.GSTAltitudeError))
	payload[124] = quality.State
	payload[125] = quality.Reason
	binary.BigEndian.PutUint16(payload[126:128], quality.Samples)
	binary.BigEndian.PutUint32(payload[128:132], quality.TimeoutMillis)
	binary.BigEndian.PutUint32(payload[132:136], math.Float32bits(quality.RMS))
	return frame(TypeStatusFull, payload)
}

func zeroUint64IfOff(mask, bit uint64, value *uint64) {
	if mask&bit == 0 {
		*value = 0
	}
}

func zeroUint8IfOff(mask, bit uint64, value *uint8) {
	if mask&bit == 0 {
		*value = 0
	}
}

func sanitizeValid(mask *uint64, bit uint64, value *uint8) {
	if *mask&bit == 0 || *value > 1 {
		*mask &^= bit
		*value = 0
	}
}

func sanitizeFixDimension(mask *uint64, bit uint64, value *uint8) {
	if *mask&bit == 0 || *value < 1 || *value > 3 {
		*mask &^= bit
		*value = 0
	}
}

func sanitizeFloat64(mask *uint64, bit uint64, value *float64, valid func(float64) bool) {
	if *mask&bit == 0 || !valid(*value) {
		*mask &^= bit
		*value = 0
	}
}

func sanitizeFloat32(mask *uint64, bit uint64, value *float32, valid func(float32) bool) {
	if *mask&bit == 0 || !valid(*value) {
		*mask &^= bit
		*value = 0
	}
}

func finite64(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finite32(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func validLatitude(value float64) bool {
	return finite64(value) && value >= -90 && value <= 90
}

func validLongitude(value float64) bool {
	return finite64(value) && value >= -180 && value <= 180
}

func nonnegative32(value float32) bool {
	return finite32(value) && value >= 0
}

func angle32(value float32) bool {
	return finite32(value) && value >= 0 && value < 360
}
