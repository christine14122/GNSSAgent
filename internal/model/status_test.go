package model

import "testing"

func TestSimpleProjectionUsesIndependentMaskBits(t *testing.T) {
	full := FullStatus{
		FieldValidityMask: FullUTCValid | FullLatitudeValid | FullAltitudeMSLValid |
			FullGroundSpeedValid | FullValidValid | FullUsedSatellitesValid,
		UTCTime:        1000,
		Latitude:       31.2,
		AltitudeMSL:    12.5,
		GroundSpeedMPS: 3.25,
		Valid:          1,
		UsedSatellites: 8,
	}
	simple := full.Simple()
	wantMask := SimpleUTCValid | SimpleLatitudeValid | SimpleAltitudeMSLValid |
		SimpleGroundSpeedValid | SimpleValidValid | SimpleUsedSatellitesValid
	if simple.FieldValidityMask != wantMask {
		t.Fatalf("mask=%#x want=%#x", simple.FieldValidityMask, wantMask)
	}
	if simple.UTCTime != 1000 || simple.Latitude != 31.2 || simple.UsedSatellites != 8 {
		t.Fatalf("unexpected projection: %+v", simple)
	}
}

func TestInvalidFieldsRemainZero(t *testing.T) {
	simple := (FullStatus{}).Simple()
	if simple != (SimpleStatus{}) {
		t.Fatalf("unexpected non-zero status: %+v", simple)
	}
}

func TestFullValidityBitsMatchWireContract(t *testing.T) {
	bits := []uint64{
		FullUTCValid,
		FullRecvValid,
		FullLatitudeValid,
		FullLongitudeValid,
		FullAltitudeMSLValid,
		FullAltitudeEllipsoidValid,
		FullValidValid,
		FullFixDimensionValid,
		FullSolutionTypeValid,
		FullUsedSatellitesValid,
		FullGPSSatellitesValid,
		FullBeiDouSatellitesValid,
		FullGLONASSSatellitesValid,
		FullGalileoSatellitesValid,
		FullGGAHDOPValid,
		FullGSAPDOPValid,
		FullGSAHDOPValid,
		FullGSAVDOPValid,
		FullDifferentialAgeValid,
		FullAvgUsedCN0Valid,
		FullGroundSpeedValid,
		FullCourseValid,
		FullGSTPseudorangeRMSValid,
		FullGSTSemiMajorValid,
		FullGSTSemiMinorValid,
		FullGSTOrientationValid,
		FullGSTLatitudeErrorValid,
		FullGSTLongitudeErrorValid,
		FullGSTAltitudeErrorValid,
		FullTimeRMSValid,
	}
	for i, bit := range bits {
		want := uint64(1) << i
		if bit != want {
			t.Errorf("FULL bit %d=%#x want=%#x", i, bit, want)
		}
	}
}

func TestSimpleValidityBitsMatchWireContract(t *testing.T) {
	bits := []uint64{
		SimpleUTCValid,
		SimpleRecvValid,
		SimpleLatitudeValid,
		SimpleLongitudeValid,
		SimpleAltitudeMSLValid,
		SimpleGroundSpeedValid,
		SimpleCourseValid,
		SimpleValidValid,
		SimpleUsedSatellitesValid,
	}
	for i, bit := range bits {
		want := uint64(1) << i
		if bit != want {
			t.Errorf("SIMPLE bit %d=%#x want=%#x", i, bit, want)
		}
	}
}

func TestSimpleProjectionCopiesEachValidField(t *testing.T) {
	full := FullStatus{
		UTCTime:             1000,
		RecvTime:            2000,
		Latitude:            31.2,
		Longitude:           121.5,
		AltitudeMSL:         12.5,
		GroundSpeedMPS:      3.25,
		CourseOverGroundDeg: 45.5,
		Valid:               1,
		UsedSatellites:      8,
	}
	tests := []struct {
		name    string
		fullBit uint64
		want    SimpleStatus
	}{
		{name: "UTC", fullBit: FullUTCValid, want: SimpleStatus{FieldValidityMask: SimpleUTCValid, UTCTime: full.UTCTime}},
		{name: "receive time", fullBit: FullRecvValid, want: SimpleStatus{FieldValidityMask: SimpleRecvValid, RecvTime: full.RecvTime}},
		{name: "latitude", fullBit: FullLatitudeValid, want: SimpleStatus{FieldValidityMask: SimpleLatitudeValid, Latitude: full.Latitude}},
		{name: "longitude", fullBit: FullLongitudeValid, want: SimpleStatus{FieldValidityMask: SimpleLongitudeValid, Longitude: full.Longitude}},
		{name: "MSL altitude", fullBit: FullAltitudeMSLValid, want: SimpleStatus{FieldValidityMask: SimpleAltitudeMSLValid, AltitudeMSL: full.AltitudeMSL}},
		{name: "ground speed", fullBit: FullGroundSpeedValid, want: SimpleStatus{FieldValidityMask: SimpleGroundSpeedValid, GroundSpeedMPS: full.GroundSpeedMPS}},
		{name: "course", fullBit: FullCourseValid, want: SimpleStatus{FieldValidityMask: SimpleCourseValid, CourseOverGroundDeg: full.CourseOverGroundDeg}},
		{name: "validity", fullBit: FullValidValid, want: SimpleStatus{FieldValidityMask: SimpleValidValid, Valid: full.Valid}},
		{name: "used satellites", fullBit: FullUsedSatellitesValid, want: SimpleStatus{FieldValidityMask: SimpleUsedSatellitesValid, UsedSatellites: full.UsedSatellites}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			full.FieldValidityMask = tt.fullBit
			if got := full.Simple(); got != tt.want {
				t.Fatalf("projection=%+v want=%+v", got, tt.want)
			}
		})
	}
}

func TestSimpleProjectionDoesNotCopyInvalidValues(t *testing.T) {
	full := FullStatus{
		UTCTime:             1000,
		RecvTime:            2000,
		Latitude:            31.2,
		Longitude:           121.5,
		AltitudeMSL:         12.5,
		GroundSpeedMPS:      3.25,
		CourseOverGroundDeg: 45.5,
		Valid:               1,
		UsedSatellites:      8,
	}
	if simple := full.Simple(); simple != (SimpleStatus{}) {
		t.Fatalf("unexpected invalid values in projection: %+v", simple)
	}
}

func TestSimpleProjectionPreservesTimeQuality(t *testing.T) {
	full := FullStatus{TimeQuality: TimeQuality{Evaluated: true, State: 2, Reason: 2, Samples: 10, RMSValid: true, RMS: 1.5, TimeoutMillis: 3000}}
	if simple := full.Simple(); simple.TimeQuality != full.TimeQuality {
		t.Fatalf("quality projection=%+v want=%+v", simple.TimeQuality, full.TimeQuality)
	}
}
