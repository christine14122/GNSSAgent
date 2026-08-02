package model

const (
	FullUTCValid uint64 = 1 << iota
	FullRecvValid
	FullLatitudeValid
	FullLongitudeValid
	FullAltitudeMSLValid
	FullAltitudeEllipsoidValid
	FullValidValid
	FullFixDimensionValid
	FullSolutionTypeValid
	FullUsedSatellitesValid
	FullGPSSatellitesValid
	FullBeiDouSatellitesValid
	FullGLONASSSatellitesValid
	FullGalileoSatellitesValid
	FullGGAHDOPValid
	FullGSAPDOPValid
	FullGSAHDOPValid
	FullGSAVDOPValid
	FullDifferentialAgeValid
	FullAvgUsedCN0Valid
	FullGroundSpeedValid
	FullCourseValid
	FullGSTPseudorangeRMSValid
	FullGSTSemiMajorValid
	FullGSTSemiMinorValid
	FullGSTOrientationValid
	FullGSTLatitudeErrorValid
	FullGSTLongitudeErrorValid
	FullGSTAltitudeErrorValid
)

const (
	SimpleUTCValid uint64 = 1 << iota
	SimpleRecvValid
	SimpleLatitudeValid
	SimpleLongitudeValid
	SimpleAltitudeMSLValid
	SimpleGroundSpeedValid
	SimpleCourseValid
	SimpleValidValid
	SimpleUsedSatellitesValid
)

type FullStatus struct {
	FieldValidityMask   uint64
	UTCTime             uint64
	RecvTime            uint64
	Latitude            float64
	Longitude           float64
	AltitudeMSL         float64
	AltitudeEllipsoid   float64
	Valid               uint8
	FixDimension        uint8
	SolutionType        uint8
	UsedSatellites      uint8
	GPSSatellites       uint8
	BeiDouSatellites    uint8
	GLONASSSatellites   uint8
	GalileoSatellites   uint8
	GGAHDOP             float32
	GSAPDOP             float32
	GSAHDOP             float32
	GSAVDOP             float32
	DifferentialAge     float32
	AvgUsedCN0          float32
	GroundSpeedMPS      float32
	CourseOverGroundDeg float32
	GSTPseudorangeRMS   float32
	GSTSemiMajorError   float32
	GSTSemiMinorError   float32
	GSTOrientationDeg   float32
	GSTLatitudeError    float32
	GSTLongitudeError   float32
	GSTAltitudeError    float32
}

type SimpleStatus struct {
	FieldValidityMask   uint64
	UTCTime             uint64
	RecvTime            uint64
	Latitude            float64
	Longitude           float64
	AltitudeMSL         float64
	GroundSpeedMPS      float32
	CourseOverGroundDeg float32
	Valid               uint8
	UsedSatellites      uint8
}

func (s FullStatus) Simple() SimpleStatus {
	var out SimpleStatus
	copyField := func(fullBit, simpleBit uint64, copyValue func()) {
		if s.FieldValidityMask&fullBit != 0 {
			out.FieldValidityMask |= simpleBit
			copyValue()
		}
	}
	copyField(FullUTCValid, SimpleUTCValid, func() { out.UTCTime = s.UTCTime })
	copyField(FullRecvValid, SimpleRecvValid, func() { out.RecvTime = s.RecvTime })
	copyField(FullLatitudeValid, SimpleLatitudeValid, func() { out.Latitude = s.Latitude })
	copyField(FullLongitudeValid, SimpleLongitudeValid, func() { out.Longitude = s.Longitude })
	copyField(FullAltitudeMSLValid, SimpleAltitudeMSLValid, func() { out.AltitudeMSL = s.AltitudeMSL })
	copyField(FullGroundSpeedValid, SimpleGroundSpeedValid, func() { out.GroundSpeedMPS = s.GroundSpeedMPS })
	copyField(FullCourseValid, SimpleCourseValid, func() { out.CourseOverGroundDeg = s.CourseOverGroundDeg })
	copyField(FullValidValid, SimpleValidValid, func() { out.Valid = s.Valid })
	copyField(FullUsedSatellitesValid, SimpleUsedSatellitesValid, func() { out.UsedSatellites = s.UsedSatellites })
	return out
}
