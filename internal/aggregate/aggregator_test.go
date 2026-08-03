package aggregate

import (
	"math"
	"testing"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
)

func TestAggregatorCycleLifecycle(t *testing.T) {
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	t.Run("next UTC second finalizes previous exactly once", func(t *testing.T) {
		a := New()
		first := rmcSentence(base, 10_000, true)
		if _, ok := a.Add(first); ok {
			t.Fatal("first sentence published")
		}
		got, ok := a.Add(rmcSentence(base.Add(time.Second), 11_000, true))
		if !ok || got.RecvTime != uint64(base.UnixMilli()) {
			t.Fatalf("transition=(%+v, %t)", got, ok)
		}
		if _, ok := a.FlushExpired(base.Add(time.Second + flushDelay)); !ok {
			t.Fatal("new cycle was not retained")
		}
		if _, ok := a.FlushExpired(base.Add(10 * time.Second)); ok {
			t.Fatal("cycle published twice")
		}
	})

	t.Run("untimed first cycle adopts first timed key", func(t *testing.T) {
		a := New()
		gsa := nmea.Sentence{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{
			FixDimension: field(uint8(3)), PRNs: []string{"01"}, PDOPText: "1", HDOPText: "1", VDOPText: "1",
		}}
		a.Add(gsa)
		if _, ok := a.Add(rmcSentence(base.Add(100*time.Millisecond), 10_000, true)); ok {
			t.Fatal("adopting a key published the untimed cycle")
		}
		got, ok := a.Add(rmcSentence(base.Add(time.Second), 11_000, true))
		if !ok || got.FixDimension != 3 || got.FieldValidityMask&model.FullFixDimensionValid == 0 {
			t.Fatalf("adopted cycle=(%+v, %t)", got, ok)
		}
	})

	t.Run("same integer second preserves all messages", func(t *testing.T) {
		a := New()
		a.Add(rmcSentence(base, 10_100, true))
		gga := nmea.Sentence{Kind: nmea.KindGGA, ReceivedAt: base.Add(100 * time.Millisecond), GGA: &nmea.GGA{
			MillisOfDay: 10_999, TimeValid: true, UsedSatellites: field(uint8(7)),
		}}
		if _, ok := a.Add(gga); ok {
			t.Fatal("same-second GGA published prematurely")
		}
		got, ok := a.FlushExpired(base.Add(flushDelay))
		if !ok || got.UsedSatellites != 7 {
			t.Fatalf("same-second status=(%+v, %t)", got, ok)
		}
	})

	t.Run("flush boundary is exact", func(t *testing.T) {
		a := New()
		a.Add(rmcSentence(base, 10_000, true))
		if _, ok := a.FlushExpired(base.Add(1499 * time.Millisecond)); ok {
			t.Fatal("flushed at 1499ms")
		}
		if _, ok := a.FlushExpired(base.Add(1500 * time.Millisecond)); !ok {
			t.Fatal("did not flush at 1500ms")
		}
	})

	t.Run("clear drops state without output", func(t *testing.T) {
		a := New()
		a.Add(rmcSentence(base, 10_000, true))
		a.Clear()
		if _, ok := a.FlushExpired(base.Add(10 * time.Second)); ok {
			t.Fatal("clear retained a cycle")
		}
	})
}

func TestFullStatusMapsEveryFieldAndValidZeros(t *testing.T) {
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	date := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	sentences := []nmea.Sentence{
		{Kind: nmea.KindRMC, Talker: "GN", ReceivedAt: base, RMC: &nmea.RMC{
			MillisOfDay: 45_322_250, TimeValid: true, Date: field(date), Status: field(byte('A')),
			Latitude: field(31.25), Longitude: field(118.75), SpeedKnots: field(1.25), CourseDeg: field(0.0),
		}},
		{Kind: nmea.KindGGA, Talker: "GN", ReceivedAt: base.Add(time.Millisecond), GGA: &nmea.GGA{
			MillisOfDay: 45_322_900, TimeValid: true, Latitude: field(32.5), Quality: field(uint8(2)),
			UsedSatellites: field(uint8(0)), HDOPText: "0.700", AltitudeMSL: field(45.8),
			GeoidSeparation: field(-0.5), DifferentialAge: field(0.0),
		}},
		gsaSentence(base, "GP", 3, nmea.Field[uint8]{}, []string{"01"}, "1.5", "0.9", "1.2"),
		gsaSentence(base, "BD", 2, nmea.Field[uint8]{}, []string{"201"}, "1.5", "0.9", "1.2"),
		gsvSentence(base, "GP", 1, 1, 1, satellite("01", 40)),
		gsvSentence(base, "BD", 1, 1, 1, satellite("201", 44)),
		gsvSentence(base, "GL", 1, 1, 0),
		gsvSentence(base, "GA", 1, 1, 1, satellite("11", 30)),
		{Kind: nmea.KindGST, Talker: "GN", ReceivedAt: base, GST: &nmea.GST{
			MillisOfDay: 45_322_100, TimeValid: true, PseudorangeRMS: field(0.0), SemiMajorError: field(1.1),
			SemiMinorError: field(0.9), OrientationDeg: field(45.0), LatitudeError: field(0.5),
			LongitudeError: field(0.6), AltitudeError: field(0.7),
		}},
	}
	got := aggregateSentences(t, sentences)

	wantMask := uint64(0)
	for _, bit := range []uint64{
		model.FullUTCValid, model.FullRecvValid, model.FullLatitudeValid, model.FullLongitudeValid,
		model.FullAltitudeMSLValid, model.FullAltitudeEllipsoidValid, model.FullValidValid,
		model.FullFixDimensionValid, model.FullSolutionTypeValid, model.FullUsedSatellitesValid,
		model.FullGPSSatellitesValid, model.FullBeiDouSatellitesValid, model.FullGLONASSSatellitesValid,
		model.FullGalileoSatellitesValid, model.FullGGAHDOPValid, model.FullGSAPDOPValid,
		model.FullGSAHDOPValid, model.FullGSAVDOPValid, model.FullDifferentialAgeValid,
		model.FullAvgUsedCN0Valid, model.FullGroundSpeedValid, model.FullCourseValid,
		model.FullGSTPseudorangeRMSValid, model.FullGSTSemiMajorValid, model.FullGSTSemiMinorValid,
		model.FullGSTOrientationValid, model.FullGSTLatitudeErrorValid, model.FullGSTLongitudeErrorValid,
		model.FullGSTAltitudeErrorValid,
	} {
		wantMask |= bit
	}
	if got.FieldValidityMask != wantMask {
		t.Fatalf("mask=%#x want=%#x missing=%#x extra=%#x", got.FieldValidityMask, wantMask, wantMask&^got.FieldValidityMask, got.FieldValidityMask&^wantMask)
	}
	wantUTC := date.Add(45_322_250 * time.Millisecond).UnixMilli()
	if got.UTCTime != uint64(wantUTC) || got.RecvTime != uint64(base.UnixMilli()) || got.Latitude != 32.5 || got.Longitude != 118.75 {
		t.Fatalf("time/position mapping: %+v", got)
	}
	if got.AltitudeMSL != 45.8 || got.AltitudeEllipsoid != 45.3 || got.Valid != 1 || got.FixDimension != 3 || got.SolutionType != 2 || got.UsedSatellites != 0 {
		t.Fatalf("solution mapping: %+v", got)
	}
	if got.GPSSatellites != 1 || got.BeiDouSatellites != 1 || got.GLONASSSatellites != 0 || got.GalileoSatellites != 1 {
		t.Fatalf("constellation counts: %+v", got)
	}
	if got.GGAHDOP != 0.7 || got.GSAPDOP != 1.5 || got.GSAHDOP != 0.9 || got.GSAVDOP != 1.2 || got.DifferentialAge != 0 || got.AvgUsedCN0 != 42 {
		t.Fatalf("quality mapping: %+v", got)
	}
	if math.Abs(float64(got.GroundSpeedMPS)-1.25*0.5144444444444445) > 1e-7 || got.CourseOverGroundDeg != 0 {
		t.Fatalf("motion mapping: %+v", got)
	}
	if got.GSTPseudorangeRMS != 0 || got.GSTSemiMajorError != 1.1 || got.GSTSemiMinorError != 0.9 || got.GSTOrientationDeg != 45 || got.GSTLatitudeError != 0.5 || got.GSTLongitudeError != 0.6 || got.GSTAltitudeError != 0.7 {
		t.Fatalf("GST mapping: %+v", got)
	}
}

func TestValidityTruthTable(t *testing.T) {
	base := time.Unix(1_000, 0)
	tests := []struct {
		name      string
		rmc       *nmea.RMC
		gga       *nmea.GGA
		wantValid uint8
		wantMask  bool
	}{
		{name: "RMC active", rmc: &nmea.RMC{Status: field(byte('A'))}, wantValid: 1, wantMask: true},
		{name: "RMC void", rmc: &nmea.RMC{Status: field(byte('V'))}, wantMask: true},
		{name: "GGA fix", gga: &nmea.GGA{Quality: field(uint8(1))}, wantValid: 1, wantMask: true},
		{name: "GGA no fix", gga: &nmea.GGA{Quality: field(uint8(0))}, wantMask: true},
		{name: "any explicit false wins", rmc: &nmea.RMC{Status: field(byte('A'))}, gga: &nmea.GGA{Quality: field(uint8(0))}, wantMask: true},
		{name: "neither explicit", rmc: &nmea.RMC{Status: field(byte('X'))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sentences []nmea.Sentence
			if tt.rmc != nil {
				sentences = append(sentences, nmea.Sentence{Kind: nmea.KindRMC, ReceivedAt: base, RMC: tt.rmc})
			}
			if tt.gga != nil {
				sentences = append(sentences, nmea.Sentence{Kind: nmea.KindGGA, ReceivedAt: base, GGA: tt.gga})
			}
			got := aggregateSentences(t, sentences)
			if got.Valid != tt.wantValid || (got.FieldValidityMask&model.FullValidValid != 0) != tt.wantMask {
				t.Fatalf("valid=%d mask=%#x", got.Valid, got.FieldValidityMask)
			}
		})
	}
}

func TestUTCRequiresDateAndTimeFromOneRMCButVoidStillPublishes(t *testing.T) {
	base := time.Unix(2_000, 0)
	date := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	got := aggregateSentences(t, []nmea.Sentence{
		{Kind: nmea.KindRMC, ReceivedAt: base, RMC: &nmea.RMC{Date: field(date)}},
		{Kind: nmea.KindRMC, ReceivedAt: base, RMC: &nmea.RMC{TimeValid: true, MillisOfDay: 1_000}},
	})
	if got.FieldValidityMask&model.FullUTCValid != 0 || got.UTCTime != 0 {
		t.Fatalf("UTC synthesized across RMCs: %+v", got)
	}
	got = aggregateSentences(t, []nmea.Sentence{{Kind: nmea.KindRMC, ReceivedAt: base, RMC: &nmea.RMC{
		Date: field(date), TimeValid: true, MillisOfDay: 1_000, Status: field(byte('V')),
	}}})
	if got.FieldValidityMask&model.FullUTCValid == 0 || got.UTCTime != uint64(date.Add(time.Second).UnixMilli()) {
		t.Fatalf("void RMC lost parseable UTC: %+v", got)
	}
}

func TestNoFixDoesNotCreateEllipsoidOrDOP(t *testing.T) {
	base := time.Unix(3_000, 0)
	got := aggregateSentences(t, []nmea.Sentence{
		{Kind: nmea.KindGGA, ReceivedAt: base, GGA: &nmea.GGA{Quality: field(uint8(0)), HDOPText: "127.000", AltitudeMSL: field(45.0), GeoidSeparation: field(0.0)}},
		gsaSentence(base, "GN", 1, nmea.Field[uint8]{}, nil, "127.000", "127.000", "127.000"),
	})
	invalid := model.FullAltitudeEllipsoidValid | model.FullGGAHDOPValid | model.FullGSAPDOPValid | model.FullGSAHDOPValid | model.FullGSAVDOPValid
	if got.FieldValidityMask&invalid != 0 || got.AltitudeEllipsoid != 0 || got.GGAHDOP != 0 || got.GSAPDOP != 0 || got.GSAHDOP != 0 || got.GSAVDOP != 0 {
		t.Fatalf("no-fix fields leaked: %+v", got)
	}
	if got.FieldValidityMask&model.FullAltitudeMSLValid == 0 || got.AltitudeMSL != 45 {
		t.Fatalf("raw MSL altitude should remain valid: %+v", got)
	}
}

func TestDOPGroupRules(t *testing.T) {
	base := time.Unix(4_000, 0)
	tests := []struct {
		name      string
		groups    [][4]string
		wantValid bool
		want      [3]float32
	}{
		{name: "one valid", groups: [][4]string{{"3", "1.5", "0.9", "1.2"}}, wantValid: true, want: [3]float32{1.5, 0.9, 1.2}},
		{name: "no valid", groups: [][4]string{{"", "1", "1", "1"}}},
		{name: "fix one", groups: [][4]string{{"1", "1", "1", "1"}}},
		{name: "sentinel member", groups: [][4]string{{"3", "1", "127.000", "1"}}},
		{name: "difference zero", groups: [][4]string{{"3", "1", "2", "3"}, {"3", "1", "2", "3"}}, wantValid: true, want: [3]float32{1, 2, 3}},
		{name: "difference ten takes first", groups: [][4]string{{"3", "0.490", "1", "2"}, {"3", "0.500", "1.010", "2.010"}}, wantValid: true, want: [3]float32{0.49, 1, 2}},
		{name: "difference eleven conflicts", groups: [][4]string{{"3", "0.489", "1", "2"}, {"3", "0.500", "1", "2"}}},
		{name: "rounded decimals conflict", groups: [][4]string{{"3", "0.4895", "1", "2"}, {"3", "0.5005", "1", "2"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sentences []nmea.Sentence
			for _, group := range tt.groups {
				var fix nmea.Field[uint8]
				if group[0] != "" {
					fix = field(uint8(group[0][0] - '0'))
				}
				sentences = append(sentences, gsaSentence(base, "GN", fix.Value, fix, nil, group[1], group[2], group[3]))
			}
			got := aggregateSentences(t, sentences)
			mask := model.FullGSAPDOPValid | model.FullGSAHDOPValid | model.FullGSAVDOPValid
			if (got.FieldValidityMask&mask == mask) != tt.wantValid || [3]float32{got.GSAPDOP, got.GSAHDOP, got.GSAVDOP} != tt.want {
				t.Fatalf("DOP=(%v,%v,%v) mask=%#x", got.GSAPDOP, got.GSAHDOP, got.GSAVDOP, got.FieldValidityMask)
			}
		})
	}
}

func TestGGAHDOPIsIndependentOfInvalidGSA(t *testing.T) {
	base := time.Unix(5_000, 0)
	got := aggregateSentences(t, []nmea.Sentence{
		{Kind: nmea.KindGGA, ReceivedAt: base, GGA: &nmea.GGA{Quality: field(uint8(1)), HDOPText: "0.0"}},
		gsaSentence(base, "GN", 1, field(uint8(1)), nil, "127.000", "127.000", "127.000"),
	})
	if got.FieldValidityMask&model.FullGGAHDOPValid == 0 || got.GGAHDOP != 0 {
		t.Fatalf("GGA HDOP=%v mask=%#x", got.GGAHDOP, got.FieldValidityMask)
	}
}

func TestCyclesDoNotCarryStaleValues(t *testing.T) {
	base := time.Unix(6_000, 0)
	a := New()
	a.Add(nmea.Sentence{Kind: nmea.KindGGA, ReceivedAt: base, GGA: &nmea.GGA{MillisOfDay: 10_000, TimeValid: true, Quality: field(uint8(1)), HDOPText: "0.8"}})
	first, ok := a.Add(rmcSentence(base.Add(time.Second), 11_000, true))
	if !ok || first.FieldValidityMask&model.FullGGAHDOPValid == 0 {
		t.Fatalf("first=%+v ok=%t", first, ok)
	}
	second, ok := a.FlushExpired(base.Add(time.Second + flushDelay))
	if !ok || second.FieldValidityMask&model.FullGGAHDOPValid != 0 || second.GGAHDOP != 0 {
		t.Fatalf("stale HDOP carried: %+v", second)
	}
}

func TestPreEpochReceiveTimeNeverWraps(t *testing.T) {
	got := aggregateSentences(t, []nmea.Sentence{{Kind: nmea.KindGSA, ReceivedAt: time.Unix(-1, 0), GSA: &nmea.GSA{}}})
	if got.RecvTime != 0 || got.FieldValidityMask&model.FullRecvValid != 0 {
		t.Fatalf("pre-epoch recv_time wrapped: %+v", got)
	}
}

func TestReviewInvalidNavigationKeepsIndependentPositionFields(t *testing.T) {
	base := time.Unix(7_000, 0)
	tests := []struct {
		name     string
		sentence nmea.Sentence
		wantLat  float64
		wantLon  float64
	}{
		{
			name: "RMC void",
			sentence: nmea.Sentence{Kind: nmea.KindRMC, ReceivedAt: base, RMC: &nmea.RMC{
				Status: field(byte('V')), Latitude: field(31.25), Longitude: field(118.75),
			}},
			wantLat: 31.25, wantLon: 118.75,
		},
		{
			name: "GGA quality zero",
			sentence: nmea.Sentence{Kind: nmea.KindGGA, ReceivedAt: base, GGA: &nmea.GGA{
				Quality: field(uint8(0)), Latitude: field(-32.5), Longitude: field(-120.5),
			}},
			wantLat: -32.5, wantLon: -120.5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateSentences(t, []nmea.Sentence{tt.sentence})
			wantMask := model.FullValidValid | model.FullLatitudeValid | model.FullLongitudeValid
			if got.FieldValidityMask&wantMask != wantMask || got.Valid != 0 || got.Latitude != tt.wantLat || got.Longitude != tt.wantLon {
				t.Fatalf("invalid navigation coupled to position: %+v", got)
			}
		})
	}
}

func TestReviewZeroGeoidMakesValidEllipsoid(t *testing.T) {
	base := time.Unix(7_100, 0)
	got := aggregateSentences(t, []nmea.Sentence{{Kind: nmea.KindGGA, ReceivedAt: base, GGA: &nmea.GGA{
		Quality: field(uint8(1)), AltitudeMSL: field(45.25), GeoidSeparation: field(0.0),
	}}})
	if got.FieldValidityMask&model.FullAltitudeEllipsoidValid == 0 || got.AltitudeEllipsoid != 45.25 {
		t.Fatalf("zero geoid treated as a placeholder despite valid fix: %+v", got)
	}
}

func TestReviewZeroGroundSpeedIsValid(t *testing.T) {
	base := time.Unix(7_200, 0)
	got := aggregateSentences(t, []nmea.Sentence{{Kind: nmea.KindRMC, ReceivedAt: base, RMC: &nmea.RMC{
		SpeedKnots: field(0.0),
	}}})
	if got.FieldValidityMask&model.FullGroundSpeedValid == 0 || got.GroundSpeedMPS != 0 {
		t.Fatalf("zero ground speed lost validity: %+v", got)
	}
}

func TestReviewGSTFieldsHaveIndependentMasksAndValues(t *testing.T) {
	base := time.Unix(7_300, 0)
	tests := []struct {
		name     string
		bit      uint64
		value    float64
		gstField func(*nmea.GST) *nmea.Field[float64]
		read     func(model.FullStatus) float32
	}{
		{name: "pseudorange RMS", bit: model.FullGSTPseudorangeRMSValid, value: 1.01, gstField: func(g *nmea.GST) *nmea.Field[float64] { return &g.PseudorangeRMS }, read: func(s model.FullStatus) float32 { return s.GSTPseudorangeRMS }},
		{name: "semi major", bit: model.FullGSTSemiMajorValid, value: 2.02, gstField: func(g *nmea.GST) *nmea.Field[float64] { return &g.SemiMajorError }, read: func(s model.FullStatus) float32 { return s.GSTSemiMajorError }},
		{name: "semi minor", bit: model.FullGSTSemiMinorValid, value: 3.03, gstField: func(g *nmea.GST) *nmea.Field[float64] { return &g.SemiMinorError }, read: func(s model.FullStatus) float32 { return s.GSTSemiMinorError }},
		{name: "orientation", bit: model.FullGSTOrientationValid, value: 40.04, gstField: func(g *nmea.GST) *nmea.Field[float64] { return &g.OrientationDeg }, read: func(s model.FullStatus) float32 { return s.GSTOrientationDeg }},
		{name: "latitude error", bit: model.FullGSTLatitudeErrorValid, value: 5.05, gstField: func(g *nmea.GST) *nmea.Field[float64] { return &g.LatitudeError }, read: func(s model.FullStatus) float32 { return s.GSTLatitudeError }},
		{name: "longitude error", bit: model.FullGSTLongitudeErrorValid, value: 6.06, gstField: func(g *nmea.GST) *nmea.Field[float64] { return &g.LongitudeError }, read: func(s model.FullStatus) float32 { return s.GSTLongitudeError }},
		{name: "altitude error", bit: model.FullGSTAltitudeErrorValid, value: 7.07, gstField: func(g *nmea.GST) *nmea.Field[float64] { return &g.AltitudeError }, read: func(s model.FullStatus) float32 { return s.GSTAltitudeError }},
	}
	const allGSTBits = model.FullGSTPseudorangeRMSValid | model.FullGSTSemiMajorValid | model.FullGSTSemiMinorValid |
		model.FullGSTOrientationValid | model.FullGSTLatitudeErrorValid | model.FullGSTLongitudeErrorValid | model.FullGSTAltitudeErrorValid
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gst := &nmea.GST{}
			*tt.gstField(gst) = field(tt.value)
			got := aggregateSentences(t, []nmea.Sentence{{Kind: nmea.KindGST, ReceivedAt: base, GST: gst}})
			if got.FieldValidityMask&allGSTBits != tt.bit || tt.read(got) != float32(tt.value) {
				t.Fatalf("GST field mask/value coupled: %+v", got)
			}
			for _, sibling := range tests {
				if sibling.bit != tt.bit && sibling.read(got) != 0 {
					t.Fatalf("invalid GST sibling %q carried value %v: %+v", sibling.name, sibling.read(got), got)
				}
			}
		})
	}
}

func TestReviewDOPInvalidInputsRemainIndependent(t *testing.T) {
	base := time.Unix(7_400, 0)
	t.Run("GGA sentinel", func(t *testing.T) {
		got := aggregateSentences(t, []nmea.Sentence{{Kind: nmea.KindGGA, ReceivedAt: base, GGA: &nmea.GGA{
			Quality: field(uint8(1)), HDOPText: "127.000",
		}}})
		if got.FieldValidityMask&model.FullGGAHDOPValid != 0 || got.GGAHDOP != 0 {
			t.Fatalf("GGA sentinel published: %+v", got)
		}
	})

	t.Run("invalid GSA group is ignored beside usable group", func(t *testing.T) {
		got := aggregateSentences(t, []nmea.Sentence{
			gsaSentence(base, "GN", 3, field(uint8(3)), nil, "bad", "0.8", "1.0"),
			gsaSentence(base, "GN", 3, field(uint8(3)), nil, "1.25", "0.75", "1.00"),
		})
		wantMask := model.FullGSAPDOPValid | model.FullGSAHDOPValid | model.FullGSAVDOPValid
		if got.FieldValidityMask&wantMask != wantMask || got.GSAPDOP != 1.25 || got.GSAHDOP != 0.75 || got.GSAVDOP != 1.0 {
			t.Fatalf("unusable group invalidated usable group: %+v", got)
		}
	})
}

func TestReviewTimedGGAAndGSTDriveCycleTransitions(t *testing.T) {
	base := time.Unix(7_500, 0)
	tests := []struct {
		name string
		make func(received time.Time, millis int64) nmea.Sentence
	}{
		{
			name: "GGA",
			make: func(received time.Time, millis int64) nmea.Sentence {
				return nmea.Sentence{Kind: nmea.KindGGA, ReceivedAt: received, GGA: &nmea.GGA{MillisOfDay: millis, TimeValid: true}}
			},
		},
		{
			name: "GST",
			make: func(received time.Time, millis int64) nmea.Sentence {
				return nmea.Sentence{Kind: nmea.KindGST, ReceivedAt: received, GST: &nmea.GST{MillisOfDay: millis, TimeValid: true}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New()
			if _, ok := a.Add(tt.make(base, 10_100)); ok {
				t.Fatal("first timed sentence published")
			}
			if _, ok := a.Add(tt.make(base.Add(100*time.Millisecond), 10_999)); ok {
				t.Fatal("same-second sentence published")
			}
			got, ok := a.Add(tt.make(base.Add(time.Second), 11_000))
			if !ok || got.RecvTime != uint64(base.UnixMilli()) {
				t.Fatalf("next-second transition=(%+v, %t)", got, ok)
			}
		})
	}
}

func aggregateSentences(t *testing.T, sentences []nmea.Sentence) model.FullStatus {
	t.Helper()
	if len(sentences) == 0 {
		t.Fatal("aggregateSentences requires input")
	}
	a := New()
	for _, sentence := range sentences {
		if _, ok := a.Add(sentence); ok {
			t.Fatal("unexpected transition while building one cycle")
		}
	}
	got, ok := a.FlushExpired(sentences[0].ReceivedAt.Add(flushDelay))
	if !ok {
		t.Fatal("cycle did not flush")
	}
	return got
}

func rmcSentence(received time.Time, millis int64, validTime bool) nmea.Sentence {
	return nmea.Sentence{Kind: nmea.KindRMC, Talker: "GN", ReceivedAt: received, RMC: &nmea.RMC{
		MillisOfDay: millis, TimeValid: validTime, Status: field(byte('A')),
	}}
}

func gsaSentence(received time.Time, talker string, fix uint8, fixField nmea.Field[uint8], prns []string, pdop, hdop, vdop string) nmea.Sentence {
	if !fixField.Valid && fix != 0 {
		fixField = field(fix)
	}
	return nmea.Sentence{Kind: nmea.KindGSA, Talker: talker, ReceivedAt: received, GSA: &nmea.GSA{
		FixDimension: fixField, PRNs: prns, PDOPText: pdop, HDOPText: hdop, VDOPText: vdop,
	}}
}

func gsvSentence(received time.Time, talker string, total, number int, visible uint8, satellites ...nmea.Satellite) nmea.Sentence {
	return nmea.Sentence{Kind: nmea.KindGSV, Talker: talker, ReceivedAt: received, GSV: &nmea.GSV{
		TotalMessages: total, MessageNumber: number, VisibleCount: field(visible), Satellites: satellites,
	}}
}

func satellite(prn string, cn0 float32) nmea.Satellite {
	return nmea.Satellite{PRN: prn, CN0: field(cn0)}
}

func field[T any](value T) nmea.Field[T] {
	return nmea.Field[T]{Value: value, Valid: true}
}
