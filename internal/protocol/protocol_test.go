package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math"
	"runtime"
	"strings"
	"testing"

	"gnssagent/internal/model"
)

const (
	allSimpleBits = uint64(1<<9) - 1
	allFullBits   = uint64(1<<30) - 1
)

func TestProtocolConstants(t *testing.T) {
	if Magic != "GNSS" || Version != 2 || HeaderSize != 8 || MaxPayload != 1024 {
		t.Fatalf("unexpected frame constants: magic=%q version=%d header=%d max=%d", Magic, Version, HeaderSize, MaxPayload)
	}
	wantTypes := []uint8{0x01, 0x02, 0x03, 0x04, 0x10, 0x11}
	gotTypes := []uint8{TypeSubscribeRequest, TypeSubscribeACK, TypeStatusFull, TypeStatusSimple, TypeSwitchRequest, TypeSwitchACK}
	if !bytes.Equal(gotTypes, wantTypes) {
		t.Fatalf("message types=%x want=%x", gotTypes, wantTypes)
	}
}

func TestProtocolEnumValues(t *testing.T) {
	tests := []struct {
		name string
		got  uint8
		want uint8
	}{
		{name: "message subscribe request", got: TypeSubscribeRequest, want: 0x01},
		{name: "message subscribe ACK", got: TypeSubscribeACK, want: 0x02},
		{name: "message status full", got: TypeStatusFull, want: 0x03},
		{name: "message status simple", got: TypeStatusSimple, want: 0x04},
		{name: "message switch request", got: TypeSwitchRequest, want: 0x10},
		{name: "message switch ACK", got: TypeSwitchACK, want: 0x11},

		{name: "status simple", got: uint8(StatusSimple), want: 1},
		{name: "status full", got: uint8(StatusFull), want: 2},

		{name: "subscribe success", got: uint8(SubscribeSuccess), want: 0},
		{name: "subscribe server full", got: uint8(SubscribeServerFull), want: 1},
		{name: "subscribe already subscribed", got: uint8(SubscribeAlreadySubscribed), want: 2},
		{name: "subscribe unsupported version", got: uint8(SubscribeUnsupportedVersion), want: 3},
		{name: "subscribe internal error", got: uint8(SubscribeInternalError), want: 4},
		{name: "subscribe invalid status type", got: uint8(SubscribeInvalidStatusType), want: 5},

		{name: "GNSS GPS", got: uint8(TypeGPS), want: 1},
		{name: "GNSS BeiDou", got: uint8(TypeBeiDou), want: 2},
		{name: "GNSS GPS plus BeiDou", got: uint8(TypeGPSBeiDou), want: 3},

		{name: "switch success", got: uint8(SwitchSuccess), want: 0},
		{name: "switch invalid argument", got: uint8(SwitchInvalidArgument), want: 1},
		{name: "switch forbidden", got: uint8(SwitchForbidden), want: 2},
		{name: "switch serial unavailable", got: uint8(SwitchSerialUnavailable), want: 3},
		{name: "switch timeout", got: uint8(SwitchTimeout), want: 4},
		{name: "switch verify failed", got: uint8(SwitchVerifyFailed), want: 5},
		{name: "switch internal error", got: uint8(SwitchInternalError), want: 6},
		{name: "switch busy", got: uint8(SwitchBusy), want: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("got=%#02x want=%#02x", tt.got, tt.want)
			}
		})
	}
}

func TestFixedFrameSizes(t *testing.T) {
	if got := len(EncodeSimple(model.SimpleStatus{})); got != 72 {
		t.Fatalf("simple frame=%d want=72", got)
	}
	if got := len(EncodeFull(model.FullStatus{})); got != 144 {
		t.Fatalf("full frame=%d want=144", got)
	}
}

func TestMessageGoldenFrames(t *testing.T) {
	tests := []struct {
		name string
		got  []byte
		want string
	}{
		{name: "simple subscribe", got: EncodeSubscribeRequest(StatusSimple), want: "474e53530201000101"},
		{name: "full subscribe", got: EncodeSubscribeRequest(StatusFull), want: "474e53530201000102"},
		{name: "subscribe success", got: EncodeSubscribeACK(SubscribeSuccess), want: "474e53530202000100"},
		{
			name: "switch request",
			got: EncodeSwitchRequest(SwitchRequest{
				RequestID: 0x01020304,
				Enabled:   1,
				Type:      TypeGPSBeiDou,
			}),
			want: "474e535302100006010203040103",
		},
		{
			name: "switch ack",
			got:  EncodeSwitchACK(SwitchACK{RequestID: 0x01020304, Result: SwitchSuccess}),
			want: "474e5353021100050102030400",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := mustHex(t, tt.want)
			if !bytes.Equal(tt.got, want) {
				t.Fatalf("got=%x want=%x", tt.got, want)
			}
		})
	}
}

func TestFrameOwnsPayload(t *testing.T) {
	payload := []byte{1}
	first := frame(TypeSubscribeRequest, payload)
	second := frame(TypeSubscribeRequest, payload)
	payload[0] = 9
	if first[8] != 1 || second[8] != 1 {
		t.Fatalf("frame aliases input payload: first=%x second=%x", first, second)
	}
	first[8] = 7
	if second[8] != 1 {
		t.Fatalf("frames alias each other: first=%x second=%x", first, second)
	}
}

func TestFramePanicsWhenPayloadExceedsProtocolMaximum(t *testing.T) {
	defer func() {
		value := recover()
		if value == nil {
			t.Fatal("frame accepted oversized payload")
		}
		message, ok := value.(string)
		if !ok || !strings.Contains(message, "payload length") || !strings.Contains(message, "1024") {
			t.Fatalf("panic=%v, want clear payload maximum message", value)
		}
	}()
	frame(TypeStatusFull, make([]byte, MaxPayload+1))
}

func TestMessagePayloadParsersPreserveRawValues(t *testing.T) {
	status, err := ParseSubscribeRequest([]byte{0xfe})
	if err != nil || status != StatusType(0xfe) {
		t.Fatalf("subscribe request: status=%d err=%v", status, err)
	}
	result, err := ParseSubscribeACK([]byte{0xfd})
	if err != nil || result != SubscribeResult(0xfd) {
		t.Fatalf("subscribe ack: result=%d err=%v", result, err)
	}
	request, err := ParseSwitchRequest([]byte{1, 2, 3, 4, 0xfc, 0xfb})
	if err != nil || request != (SwitchRequest{RequestID: 0x01020304, Enabled: 0xfc, Type: 0xfb}) {
		t.Fatalf("switch request: request=%+v err=%v", request, err)
	}
	ack, err := ParseSwitchACK([]byte{1, 2, 3, 4, 0xfa})
	if err != nil || ack != (SwitchACK{RequestID: 0x01020304, Result: 0xfa}) {
		t.Fatalf("switch ack: ack=%+v err=%v", ack, err)
	}
}

func TestSwitchEnumsUseStrongTypesAndPreserveUnknownValues(t *testing.T) {
	request := SwitchRequest{
		RequestID: 0x01020304,
		Enabled:   0xfc,
		Type:      SwitchType(0xfb),
	}
	parsedRequest, err := ParseSwitchRequest(EncodeSwitchRequest(request)[HeaderSize:])
	if err != nil || parsedRequest != request {
		t.Fatalf("switch request: parsed=%+v want=%+v err=%v", parsedRequest, request, err)
	}

	ack := SwitchACK{RequestID: 0x05060708, Result: SwitchResult(0xfa)}
	parsedACK, err := ParseSwitchACK(EncodeSwitchACK(ack)[HeaderSize:])
	if err != nil || parsedACK != ack {
		t.Fatalf("switch ACK: parsed=%+v want=%+v err=%v", parsedACK, ack, err)
	}
}

func TestMessagePayloadParsersRejectEveryWrongLength(t *testing.T) {
	for _, length := range []int{0, 2, 6} {
		if _, err := ParseSubscribeRequest(make([]byte, length)); err == nil {
			t.Errorf("subscribe request accepted length %d", length)
		}
		if _, err := ParseSubscribeACK(make([]byte, length)); err == nil {
			t.Errorf("subscribe ack accepted length %d", length)
		}
	}
	for _, length := range []int{0, 5, 7} {
		if _, err := ParseSwitchRequest(make([]byte, length)); err == nil {
			t.Errorf("switch request accepted length %d", length)
		}
	}
	for _, length := range []int{0, 4, 6} {
		if _, err := ParseSwitchACK(make([]byte, length)); err == nil {
			t.Errorf("switch ack accepted length %d", length)
		}
	}
}

func TestSimpleAllFieldOffsetsAndMaskBits(t *testing.T) {
	assertSimpleMaskBits(t)
	status := model.SimpleStatus{
		FieldValidityMask:   allSimpleBits,
		UTCTime:             0x0102030405060708,
		RecvTime:            0x1112131415161718,
		Latitude:            12.25,
		Longitude:           -34.5,
		AltitudeMSL:         -56.75,
		GroundSpeedMPS:      7.5,
		CourseOverGroundDeg: 89.25,
		Valid:               1,
		UsedSatellites:      9,
	}
	frame := EncodeSimple(status)
	if !bytes.Equal(frame[:8], mustHex(t, "474e535302040040")) {
		t.Fatalf("header=%x", frame[:8])
	}
	payload := frame[8:]
	assertUint64(t, payload, 0, allSimpleBits)
	assertUint64(t, payload, 8, 0x0102030405060708)
	assertUint64(t, payload, 16, 0x1112131415161718)
	assertFloat64(t, payload, 24, 12.25)
	assertFloat64(t, payload, 32, -34.5)
	assertFloat64(t, payload, 40, -56.75)
	assertFloat32(t, payload, 48, 7.5)
	assertFloat32(t, payload, 52, 89.25)
	if payload[56] != 1 || payload[57] != 9 {
		t.Fatalf("tail=%x want=0109", payload[56:58])
	}
	assertLayoutEnd(t, []int{8, 8, 8, 8, 8, 8, 4, 4, 1, 1, 1, 1, 4}, 64)
}

func TestFullAllFieldOffsetsAndMaskBits(t *testing.T) {
	assertFullMaskBits(t)
	status := validFullStatus()
	frame := EncodeFull(status)
	if !bytes.Equal(frame[:8], mustHex(t, "474e535302030088")) {
		t.Fatalf("header=%x", frame[:8])
	}
	payload := frame[8:]
	assertUint64(t, payload, 0, allFullBits)
	assertUint64(t, payload, 8, 0x0102030405060708)
	assertUint64(t, payload, 16, 0x1112131415161718)
	assertFloat64(t, payload, 24, 12.25)
	assertFloat64(t, payload, 32, -34.5)
	assertFloat64(t, payload, 40, -56.75)
	assertFloat64(t, payload, 48, -78.125)
	if !bytes.Equal(payload[56:64], []byte{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("uint8 fields=%x", payload[56:64])
	}
	values := []float32{
		1.25, 2.5, 3.75, 4.125, 5.5, 6.625, 7.75, 8.875,
		9.25, 10.5, 11.75, 12.875, 13.25, 14.5, 15.75,
	}
	for i, want := range values {
		assertFloat32(t, payload, 64+i*4, want)
	}
	assertLayoutEnd(t, []int{
		8, 8, 8, 8, 8, 8, 8,
		1, 1, 1, 1, 1, 1, 1, 1,
		4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
		1, 1, 2, 4, 4,
	}, 136)
}

func TestStatusValidZeroFieldsKeepTheirBits(t *testing.T) {
	simple := EncodeSimple(model.SimpleStatus{FieldValidityMask: model.SimpleValidValid | model.SimpleUsedSatellitesValid})[8:]
	if got := binary.BigEndian.Uint64(simple[:8]); got != model.SimpleValidValid|model.SimpleUsedSatellitesValid {
		t.Fatalf("simple mask=%#x", got)
	}
	if simple[56] != 0 || simple[57] != 0 {
		t.Fatalf("simple zero fields=%x", simple[56:58])
	}

	full := EncodeFull(model.FullStatus{
		FieldValidityMask: model.FullValidValid | model.FullSolutionTypeValid | model.FullDifferentialAgeValid,
	})[8:]
	wantMask := model.FullValidValid | model.FullSolutionTypeValid | model.FullDifferentialAgeValid
	if got := binary.BigEndian.Uint64(full[:8]); got != wantMask {
		t.Fatalf("full mask=%#x want=%#x", got, wantMask)
	}
	if full[56] != 0 || full[58] != 0 || binary.BigEndian.Uint32(full[80:84]) != 0 {
		t.Fatalf("full valid zeros changed: valid=%d solution=%d age=%x", full[56], full[58], full[80:84])
	}
}

func TestFullFixDimensionSanitization(t *testing.T) {
	tests := []struct {
		name     string
		mask     uint64
		value    uint8
		wantMask uint64
		wantByte uint8
	}{
		{name: "bit off", mask: 0, value: 3, wantMask: 0, wantByte: 0},
		{name: "zero invalid", mask: model.FullFixDimensionValid, value: 0, wantMask: 0, wantByte: 0},
		{name: "one valid", mask: model.FullFixDimensionValid, value: 1, wantMask: model.FullFixDimensionValid, wantByte: 1},
		{name: "two valid", mask: model.FullFixDimensionValid, value: 2, wantMask: model.FullFixDimensionValid, wantByte: 2},
		{name: "three valid", mask: model.FullFixDimensionValid, value: 3, wantMask: model.FullFixDimensionValid, wantByte: 3},
		{name: "four invalid", mask: model.FullFixDimensionValid, value: 4, wantMask: 0, wantByte: 0},
		{name: "maximum invalid", mask: model.FullFixDimensionValid, value: 255, wantMask: 0, wantByte: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := EncodeFull(model.FullStatus{
				FieldValidityMask: tt.mask,
				FixDimension:      tt.value,
			})[HeaderSize:]
			if got := binary.BigEndian.Uint64(payload[0:8]); got != tt.wantMask {
				t.Fatalf("mask=%#x want=%#x", got, tt.wantMask)
			}
			if got := payload[57]; got != tt.wantByte {
				t.Fatalf("fix dimension=%d want=%d", got, tt.wantByte)
			}
		})
	}
}

func TestStatusBitsOffForceNonzeroValuesToZero(t *testing.T) {
	simpleStatus := model.SimpleStatus{
		UTCTime:             1,
		RecvTime:            2,
		Latitude:            3,
		Longitude:           4,
		AltitudeMSL:         5,
		GroundSpeedMPS:      6,
		CourseOverGroundDeg: 7,
		Valid:               1,
		UsedSatellites:      8,
	}
	if payload := EncodeSimple(simpleStatus)[8:]; !allZero(payload) {
		t.Fatalf("simple invalid fields leaked: %x", payload)
	}

	fullStatus := validFullStatus()
	fullStatus.FieldValidityMask = 0
	fullStatus.TimeQuality = model.TimeQuality{}
	if payload := EncodeFull(fullStatus)[8:]; !allZero(payload) {
		t.Fatalf("full invalid fields leaked: %x", payload)
	}
}

func TestStatusReservedMaskBitsAreCleared(t *testing.T) {
	simple := validSimpleStatus()
	simple.FieldValidityMask = ^uint64(0)
	if got := binary.BigEndian.Uint64(EncodeSimple(simple)[8:16]); got != allSimpleBits {
		t.Fatalf("simple mask=%#x want=%#x", got, allSimpleBits)
	}
	full := validFullStatus()
	full.FieldValidityMask = ^uint64(0)
	if got := binary.BigEndian.Uint64(EncodeFull(full)[8:16]); got != allFullBits {
		t.Fatalf("full mask=%#x want=%#x", got, allFullBits)
	}
}

func TestEncodeSimpleDoesNotMutateSource(t *testing.T) {
	status := model.SimpleStatus{
		FieldValidityMask:   ^uint64(0),
		UTCTime:             1,
		RecvTime:            2,
		Latitude:            91,
		Longitude:           -181,
		AltitudeMSL:         -3,
		GroundSpeedMPS:      -4,
		CourseOverGroundDeg: 360,
		Valid:               2,
		UsedSatellites:      5,
	}
	want := status
	EncodeSimple(status)
	if status != want {
		t.Fatalf("source mutated: got=%+v want=%+v", status, want)
	}
}

func TestEncodeFullDoesNotMutateSource(t *testing.T) {
	status := validFullStatus()
	status.FieldValidityMask = ^uint64(0)
	status.Latitude = 91
	status.Longitude = -181
	status.Valid = 2
	status.FixDimension = 255
	status.GGAHDOP = -1
	status.CourseOverGroundDeg = 360
	status.GSTOrientationDeg = 360
	want := status
	EncodeFull(status)
	if status != want {
		t.Fatalf("source mutated: got=%+v want=%+v", status, want)
	}
}

func TestSimpleInvalidValuesClearOnlyMatchingBitAndValue(t *testing.T) {
	tests := []struct {
		name   string
		bit    uint64
		offset int
		size   int
		mutate func(*model.SimpleStatus)
	}{
		{name: "latitude range", bit: model.SimpleLatitudeValid, offset: 24, size: 8, mutate: func(s *model.SimpleStatus) { s.Latitude = 90.01 }},
		{name: "longitude range", bit: model.SimpleLongitudeValid, offset: 32, size: 8, mutate: func(s *model.SimpleStatus) { s.Longitude = -180.01 }},
		{name: "altitude NaN", bit: model.SimpleAltitudeMSLValid, offset: 40, size: 8, mutate: func(s *model.SimpleStatus) { s.AltitudeMSL = math.NaN() }},
		{name: "speed negative", bit: model.SimpleGroundSpeedValid, offset: 48, size: 4, mutate: func(s *model.SimpleStatus) { s.GroundSpeedMPS = -0.01 }},
		{name: "course infinity", bit: model.SimpleCourseValid, offset: 52, size: 4, mutate: func(s *model.SimpleStatus) { s.CourseOverGroundDeg = float32(math.Inf(1)) }},
		{name: "course range", bit: model.SimpleCourseValid, offset: 52, size: 4, mutate: func(s *model.SimpleStatus) { s.CourseOverGroundDeg = 360 }},
		{name: "valid range", bit: model.SimpleValidValid, offset: 56, size: 1, mutate: func(s *model.SimpleStatus) { s.Valid = 2 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := validSimpleStatus()
			tt.mutate(&status)
			payload := EncodeSimple(status)[8:]
			if got, want := binary.BigEndian.Uint64(payload[:8]), allSimpleBits&^tt.bit; got != want {
				t.Fatalf("mask=%#x want=%#x", got, want)
			}
			if !allZero(payload[tt.offset : tt.offset+tt.size]) {
				t.Fatalf("invalid field bytes=%x", payload[tt.offset:tt.offset+tt.size])
			}
		})
	}
}

func TestFullInvalidValuesClearOnlyMatchingBitAndValue(t *testing.T) {
	tests := []struct {
		name   string
		bit    uint64
		offset int
		size   int
		mutate func(*model.FullStatus)
	}{
		{name: "latitude range", bit: model.FullLatitudeValid, offset: 24, size: 8, mutate: func(s *model.FullStatus) { s.Latitude = 91 }},
		{name: "longitude range", bit: model.FullLongitudeValid, offset: 32, size: 8, mutate: func(s *model.FullStatus) { s.Longitude = -181 }},
		{name: "MSL altitude NaN", bit: model.FullAltitudeMSLValid, offset: 40, size: 8, mutate: func(s *model.FullStatus) { s.AltitudeMSL = math.NaN() }},
		{name: "ellipsoid infinity", bit: model.FullAltitudeEllipsoidValid, offset: 48, size: 8, mutate: func(s *model.FullStatus) { s.AltitudeEllipsoid = math.Inf(-1) }},
		{name: "valid range", bit: model.FullValidValid, offset: 56, size: 1, mutate: func(s *model.FullStatus) { s.Valid = 2 }},
		{name: "GGA HDOP negative", bit: model.FullGGAHDOPValid, offset: 64, size: 4, mutate: func(s *model.FullStatus) { s.GGAHDOP = -1 }},
		{name: "GSA PDOP NaN", bit: model.FullGSAPDOPValid, offset: 68, size: 4, mutate: func(s *model.FullStatus) { s.GSAPDOP = float32(math.NaN()) }},
		{name: "GSA HDOP infinity", bit: model.FullGSAHDOPValid, offset: 72, size: 4, mutate: func(s *model.FullStatus) { s.GSAHDOP = float32(math.Inf(1)) }},
		{name: "GSA VDOP negative", bit: model.FullGSAVDOPValid, offset: 76, size: 4, mutate: func(s *model.FullStatus) { s.GSAVDOP = -1 }},
		{name: "differential age negative", bit: model.FullDifferentialAgeValid, offset: 80, size: 4, mutate: func(s *model.FullStatus) { s.DifferentialAge = -1 }},
		{name: "average CN0 negative", bit: model.FullAvgUsedCN0Valid, offset: 84, size: 4, mutate: func(s *model.FullStatus) { s.AvgUsedCN0 = -1 }},
		{name: "speed negative", bit: model.FullGroundSpeedValid, offset: 88, size: 4, mutate: func(s *model.FullStatus) { s.GroundSpeedMPS = -1 }},
		{name: "course range", bit: model.FullCourseValid, offset: 92, size: 4, mutate: func(s *model.FullStatus) { s.CourseOverGroundDeg = 360 }},
		{name: "pseudorange RMS negative", bit: model.FullGSTPseudorangeRMSValid, offset: 96, size: 4, mutate: func(s *model.FullStatus) { s.GSTPseudorangeRMS = -1 }},
		{name: "semi major NaN", bit: model.FullGSTSemiMajorValid, offset: 100, size: 4, mutate: func(s *model.FullStatus) { s.GSTSemiMajorError = float32(math.NaN()) }},
		{name: "semi minor infinity", bit: model.FullGSTSemiMinorValid, offset: 104, size: 4, mutate: func(s *model.FullStatus) { s.GSTSemiMinorError = float32(math.Inf(1)) }},
		{name: "orientation range", bit: model.FullGSTOrientationValid, offset: 108, size: 4, mutate: func(s *model.FullStatus) { s.GSTOrientationDeg = 360 }},
		{name: "latitude error negative", bit: model.FullGSTLatitudeErrorValid, offset: 112, size: 4, mutate: func(s *model.FullStatus) { s.GSTLatitudeError = -1 }},
		{name: "longitude error NaN", bit: model.FullGSTLongitudeErrorValid, offset: 116, size: 4, mutate: func(s *model.FullStatus) { s.GSTLongitudeError = float32(math.NaN()) }},
		{name: "altitude error infinity", bit: model.FullGSTAltitudeErrorValid, offset: 120, size: 4, mutate: func(s *model.FullStatus) { s.GSTAltitudeError = float32(math.Inf(-1)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := validFullStatus()
			tt.mutate(&status)
			payload := EncodeFull(status)[8:]
			if got, want := binary.BigEndian.Uint64(payload[:8]), allFullBits&^tt.bit; got != want {
				t.Fatalf("mask=%#x want=%#x", got, want)
			}
			if !allZero(payload[tt.offset : tt.offset+tt.size]) {
				t.Fatalf("invalid field bytes=%x", payload[tt.offset:tt.offset+tt.size])
			}
		})
	}
}

func TestStatusBoundaryValuesAndNegativeAltitudesRemainValid(t *testing.T) {
	simple := model.SimpleStatus{
		FieldValidityMask:   model.SimpleLatitudeValid | model.SimpleLongitudeValid | model.SimpleAltitudeMSLValid | model.SimpleGroundSpeedValid | model.SimpleCourseValid,
		Latitude:            -90,
		Longitude:           180,
		AltitudeMSL:         -1,
		GroundSpeedMPS:      0,
		CourseOverGroundDeg: math.SmallestNonzeroFloat32,
	}
	if got := binary.BigEndian.Uint64(EncodeSimple(simple)[8:16]); got != simple.FieldValidityMask {
		t.Fatalf("simple boundary mask=%#x want=%#x", got, simple.FieldValidityMask)
	}

	full := model.FullStatus{
		FieldValidityMask: model.FullLatitudeValid | model.FullLongitudeValid | model.FullAltitudeMSLValid |
			model.FullAltitudeEllipsoidValid | model.FullGSTOrientationValid,
		Latitude:          90,
		Longitude:         -180,
		AltitudeMSL:       -2,
		AltitudeEllipsoid: -3,
		GSTOrientationDeg: 359.999,
	}
	if got := binary.BigEndian.Uint64(EncodeFull(full)[8:16]); got != full.FieldValidityMask {
		t.Fatalf("full boundary mask=%#x want=%#x", got, full.FieldValidityMask)
	}
}

func TestSimpleAndFullMasksAreIndependent(t *testing.T) {
	simple := EncodeSimple(model.SimpleStatus{
		FieldValidityMask: model.SimpleGroundSpeedValid,
		GroundSpeedMPS:    12.5,
	})[8:]
	if got := binary.BigEndian.Uint64(simple[:8]); got != 1<<5 {
		t.Fatalf("simple mask=%#x want bit 5", got)
	}
	assertFloat32(t, simple, 48, 12.5)

	full := EncodeFull(model.FullStatus{
		FieldValidityMask: model.SimpleGroundSpeedValid,
		AltitudeEllipsoid: 22.5,
		GroundSpeedMPS:    12.5,
	})[8:]
	if got := binary.BigEndian.Uint64(full[:8]); got != model.FullAltitudeEllipsoidValid {
		t.Fatalf("full mask=%#x want altitude ellipsoid bit", got)
	}
	assertFloat64(t, full, 48, 22.5)
	if !allZero(full[88:92]) {
		t.Fatalf("FULL interpreted SIMPLE bit 5 as speed: %x", full[88:92])
	}

	full = EncodeFull(model.FullStatus{
		FieldValidityMask: model.FullGroundSpeedValid,
		GroundSpeedMPS:    12.5,
	})[8:]
	if got := binary.BigEndian.Uint64(full[:8]); got != 1<<20 {
		t.Fatalf("full speed mask=%#x want bit 20", got)
	}
	assertFloat32(t, full, 88, 12.5)
}

func TestDecoderHandlesGarbagePartialHeaderAndStickyFrames(t *testing.T) {
	d := NewDecoder(1024)
	first := EncodeSubscribeRequest(StatusSimple)
	second := EncodeSwitchRequest(SwitchRequest{RequestID: 7, Enabled: 0, Type: TypeGPS})
	if frames := d.Feed(append([]byte("garbage"), first[:5]...)); len(frames) != 0 {
		t.Fatalf("unexpected frames: %v", frames)
	}
	frames := d.Feed(append(first[5:], second...))
	if len(frames) != 2 {
		t.Fatalf("frames=%d want=2", len(frames))
	}
	if frames[0].Type != TypeSubscribeRequest || !bytes.Equal(frames[0].Payload, []byte{byte(StatusSimple)}) {
		t.Fatalf("first=%+v", frames[0])
	}
	if frames[1].Type != TypeSwitchRequest || !bytes.Equal(frames[1].Payload, []byte{0, 0, 0, 7, 0, byte(TypeGPS)}) {
		t.Fatalf("second=%+v", frames[1])
	}
}

func TestDecoderHandlesEveryMagicSplitPosition(t *testing.T) {
	frame := EncodeSubscribeRequest(StatusFull)
	for split := 0; split <= len(Magic); split++ {
		t.Run(string(rune('0'+split)), func(t *testing.T) {
			d := NewDecoder(MaxPayload)
			if got := d.Feed(frame[:split]); len(got) != 0 {
				t.Fatalf("split %d emitted early: %+v", split, got)
			}
			got := d.Feed(frame[split:])
			if len(got) != 1 || got[0].Type != TypeSubscribeRequest {
				t.Fatalf("split %d frames=%+v", split, got)
			}
		})
	}
}

func TestDecoderResynchronizesAfterOversizedLength(t *testing.T) {
	d := NewDecoder(1024)
	bad := []byte{'G', 'N', 'S', 'S', 1, 1, 0x04, 0x01}
	frames := d.Feed(append(bad, EncodeSubscribeRequest(StatusFull)...))
	if len(frames) != 1 || frames[0].Type != TypeSubscribeRequest || !bytes.Equal(frames[0].Payload, []byte{2}) {
		t.Fatalf("failed to resynchronize: %+v", frames)
	}
}

func TestDecoderFindsMagicInsideOversizedCandidateHeader(t *testing.T) {
	d := NewDecoder(MaxPayload)
	valid := EncodeSubscribeRequest(StatusFull)
	input := append([]byte{'G', 'N', 'S', 'S', Version, 0x99}, valid...)
	frames := d.Feed(input)
	if len(frames) != 1 || frames[0].Type != TypeSubscribeRequest || !bytes.Equal(frames[0].Payload, []byte{byte(StatusFull)}) {
		t.Fatalf("frames=%+v", frames)
	}
}

func TestDecoderDiscardsWholeKnownWrongLengthFrameContainingMagic(t *testing.T) {
	d := NewDecoder(MaxPayload)
	embedded := EncodeSubscribeRequest(StatusFull)
	bad := rawFrame(Version, TypeSubscribeRequest, append([]byte{0xff}, embedded...))
	following := EncodeSubscribeRequest(StatusSimple)
	frames := d.Feed(append(bad, following...))
	if len(frames) != 1 || frames[0].Type != TypeSubscribeRequest || !bytes.Equal(frames[0].Payload, []byte{byte(StatusSimple)}) {
		t.Fatalf("frames=%+v", frames)
	}
}

func TestDecoderDropsKnownVersionTwoFramesWithWrongLengths(t *testing.T) {
	wants := []struct {
		messageType uint8
		length      int
	}{
		{TypeSubscribeRequest, 1},
		{TypeSubscribeACK, 1},
		{TypeStatusFull, 136},
		{TypeStatusSimple, 64},
		{TypeSwitchRequest, 6},
		{TypeSwitchACK, 5},
	}
	valid := EncodeSubscribeRequest(StatusSimple)
	for _, tt := range wants {
		t.Run(hex.EncodeToString([]byte{tt.messageType}), func(t *testing.T) {
			d := NewDecoder(MaxPayload)
			bad := rawFrame(Version, tt.messageType, make([]byte, tt.length+1))
			frames := d.Feed(append(bad, valid...))
			if len(frames) != 1 || frames[0].Type != TypeSubscribeRequest {
				t.Fatalf("type=%#x frames=%+v", tt.messageType, frames)
			}
		})
	}
}

func TestDecoderRetainsUnknownTypeAndUnsupportedVersion(t *testing.T) {
	d := NewDecoder(MaxPayload)
	unknown := rawFrame(Version, 0x99, []byte{1, 2})
	unsupported := rawFrame(1, TypeSubscribeRequest, []byte{3, 4, 5})
	frames := d.Feed(append(unknown, unsupported...))
	if len(frames) != 2 {
		t.Fatalf("frames=%+v", frames)
	}
	if frames[0].Version != Version || frames[0].Type != 0x99 || !bytes.Equal(frames[0].Payload, []byte{1, 2}) {
		t.Fatalf("unknown=%+v", frames[0])
	}
	if frames[1].Version != 1 || frames[1].Type != TypeSubscribeRequest || !bytes.Equal(frames[1].Payload, []byte{3, 4, 5}) {
		t.Fatalf("unsupported=%+v", frames[1])
	}
}

func TestDecoderPayloadOwnershipSurvivesInputMutationAndFutureFeed(t *testing.T) {
	d := NewDecoder(MaxPayload)
	input := rawFrame(Version, 0x99, []byte{1, 2, 3})
	frames := d.Feed(input)
	if len(frames) != 1 {
		t.Fatalf("frames=%+v", frames)
	}
	input[8] = 9
	d.Feed([]byte("future garbage"))
	if !bytes.Equal(frames[0].Payload, []byte{1, 2, 3}) {
		t.Fatalf("payload was mutated: %x", frames[0].Payload)
	}
}

func TestDecoderGarbageAndPartialFramesStayBounded(t *testing.T) {
	d := NewDecoder(MaxPayload)
	for i := 0; i < 1000; i++ {
		d.Feed([]byte{'x'})
		if len(d.buf) > 3 {
			t.Fatalf("garbage buffer=%d", len(d.buf))
		}
	}
	header := rawFrame(Version, 0x99, make([]byte, MaxPayload))[:HeaderSize]
	partial := append(header, make([]byte, MaxPayload-1)...)
	if frames := d.Feed(partial); len(frames) != 0 {
		t.Fatalf("partial emitted: %+v", frames)
	}
	if len(d.buf) > HeaderSize+MaxPayload {
		t.Fatalf("partial buffer=%d", len(d.buf))
	}
	frames := d.Feed([]byte{0})
	if len(frames) != 1 || len(frames[0].Payload) != MaxPayload {
		t.Fatalf("completed max frame=%+v", frames)
	}

	d = NewDecoder(MaxPayload)
	oversized := []byte{'G', 'N', 'S', 'S', Version, 0x99, 0x04, 0x01}
	for i := 0; i < 1000; i++ {
		d.Feed(oversized)
		if len(d.buf) > 3 {
			t.Fatalf("oversized buffer=%d after iteration %d", len(d.buf), i)
		}
	}
}

func TestDecoderLargeGarbageDoesNotAllocateInputSizedBuffer(t *testing.T) {
	input := bytes.Repeat([]byte{'x'}, 8<<20)
	d := NewDecoder(MaxPayload)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	frames := d.Feed(input)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(input)

	if len(frames) != 0 {
		t.Fatalf("garbage emitted frames: %+v", frames)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("Feed allocated %d bytes for large garbage input", allocated)
	}
	assertDecoderBufferBound(t, d)
}

func TestDecoderLargeAdversarialMagicPrefixesStayBounded(t *testing.T) {
	pattern := []byte{'G', 'N', 'S', 'S', Version, 0x99, 0x04, 0x01, 'G', 'N', 'S', 'X'}
	input := bytes.Repeat(pattern, 200_000)
	d := NewDecoder(MaxPayload)
	if frames := d.Feed(input); len(frames) != 0 {
		t.Fatalf("adversarial garbage emitted frames: %+v", frames)
	}
	assertDecoderBufferBound(t, d)
}

func TestDecoderMaximumConfigurationDefaultsAndCaps(t *testing.T) {
	for _, configured := range []int{0, -1, MaxPayload + 1} {
		d := NewDecoder(configured)
		if d.maxPayload != MaxPayload {
			t.Fatalf("configured=%d effective=%d", configured, d.maxPayload)
		}
	}
	d := NewDecoder(4)
	if d.maxPayload != 4 {
		t.Fatalf("effective=%d want=4", d.maxPayload)
	}
	tooLarge := rawFrame(Version, 0x99, make([]byte, 5))
	frames := d.Feed(append(tooLarge, EncodeSubscribeRequest(StatusSimple)...))
	if len(frames) != 1 || frames[0].Type != TypeSubscribeRequest {
		t.Fatalf("configured maximum not enforced: %+v", frames)
	}
}

func validSimpleStatus() model.SimpleStatus {
	return model.SimpleStatus{
		FieldValidityMask:   allSimpleBits,
		UTCTime:             1,
		RecvTime:            2,
		Latitude:            3,
		Longitude:           4,
		AltitudeMSL:         -5,
		GroundSpeedMPS:      6,
		CourseOverGroundDeg: 7,
		Valid:               1,
		UsedSatellites:      8,
	}
}

func validFullStatus() model.FullStatus {
	return model.FullStatus{
		FieldValidityMask:   allFullBits,
		UTCTime:             0x0102030405060708,
		RecvTime:            0x1112131415161718,
		Latitude:            12.25,
		Longitude:           -34.5,
		AltitudeMSL:         -56.75,
		AltitudeEllipsoid:   -78.125,
		Valid:               1,
		FixDimension:        2,
		SolutionType:        3,
		UsedSatellites:      4,
		GPSSatellites:       5,
		BeiDouSatellites:    6,
		GLONASSSatellites:   7,
		GalileoSatellites:   8,
		GGAHDOP:             1.25,
		GSAPDOP:             2.5,
		GSAHDOP:             3.75,
		GSAVDOP:             4.125,
		DifferentialAge:     5.5,
		AvgUsedCN0:          6.625,
		GroundSpeedMPS:      7.75,
		CourseOverGroundDeg: 8.875,
		GSTPseudorangeRMS:   9.25,
		GSTSemiMajorError:   10.5,
		GSTSemiMinorError:   11.75,
		GSTOrientationDeg:   12.875,
		GSTLatitudeError:    13.25,
		GSTLongitudeError:   14.5,
		GSTAltitudeError:    15.75,
		TimeQuality:         model.TimeQuality{Evaluated: true, RMSValid: true, RMS: 1.5},
	}
}

func assertSimpleMaskBits(t *testing.T) {
	t.Helper()
	got := []uint64{
		model.SimpleUTCValid,
		model.SimpleRecvValid,
		model.SimpleLatitudeValid,
		model.SimpleLongitudeValid,
		model.SimpleAltitudeMSLValid,
		model.SimpleGroundSpeedValid,
		model.SimpleCourseValid,
		model.SimpleValidValid,
		model.SimpleUsedSatellitesValid,
	}
	for i, bit := range got {
		if want := uint64(1) << i; bit != want {
			t.Fatalf("simple bit %d=%#x want=%#x", i, bit, want)
		}
	}
}

func assertFullMaskBits(t *testing.T) {
	t.Helper()
	got := []uint64{
		model.FullUTCValid,
		model.FullRecvValid,
		model.FullLatitudeValid,
		model.FullLongitudeValid,
		model.FullAltitudeMSLValid,
		model.FullAltitudeEllipsoidValid,
		model.FullValidValid,
		model.FullFixDimensionValid,
		model.FullSolutionTypeValid,
		model.FullUsedSatellitesValid,
		model.FullGPSSatellitesValid,
		model.FullBeiDouSatellitesValid,
		model.FullGLONASSSatellitesValid,
		model.FullGalileoSatellitesValid,
		model.FullGGAHDOPValid,
		model.FullGSAPDOPValid,
		model.FullGSAHDOPValid,
		model.FullGSAVDOPValid,
		model.FullDifferentialAgeValid,
		model.FullAvgUsedCN0Valid,
		model.FullGroundSpeedValid,
		model.FullCourseValid,
		model.FullGSTPseudorangeRMSValid,
		model.FullGSTSemiMajorValid,
		model.FullGSTSemiMinorValid,
		model.FullGSTOrientationValid,
		model.FullGSTLatitudeErrorValid,
		model.FullGSTLongitudeErrorValid,
		model.FullGSTAltitudeErrorValid,
		model.FullTimeRMSValid,
	}
	for i, bit := range got {
		if want := uint64(1) << i; bit != want {
			t.Fatalf("full bit %d=%#x want=%#x", i, bit, want)
		}
	}
}

func assertLayoutEnd(t *testing.T, widths []int, want int) {
	t.Helper()
	cursor := 0
	for _, width := range widths {
		cursor += width
	}
	if cursor != want {
		t.Fatalf("layout end=%d want=%d", cursor, want)
	}
}

func assertDecoderBufferBound(t *testing.T, decoder *Decoder) {
	t.Helper()
	limit := HeaderSize + decoder.maxPayload
	if len(decoder.buf) > limit || cap(decoder.buf) > limit {
		t.Fatalf("decoder buffer len=%d cap=%d limit=%d", len(decoder.buf), cap(decoder.buf), limit)
	}
}

func assertUint64(t *testing.T, payload []byte, offset int, want uint64) {
	t.Helper()
	if got := binary.BigEndian.Uint64(payload[offset : offset+8]); got != want {
		t.Fatalf("uint64 offset %d=%#x want=%#x", offset, got, want)
	}
}

func assertFloat64(t *testing.T, payload []byte, offset int, want float64) {
	t.Helper()
	if got := binary.BigEndian.Uint64(payload[offset : offset+8]); got != math.Float64bits(want) {
		t.Fatalf("float64 offset %d bits=%#x want=%#x", offset, got, math.Float64bits(want))
	}
}

func assertFloat32(t *testing.T, payload []byte, offset int, want float32) {
	t.Helper()
	if got := binary.BigEndian.Uint32(payload[offset : offset+4]); got != math.Float32bits(want) {
		t.Fatalf("float32 offset %d bits=%#x want=%#x", offset, got, math.Float32bits(want))
	}
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func rawFrame(version, messageType uint8, payload []byte) []byte {
	out := make([]byte, HeaderSize+len(payload))
	copy(out, []byte{'G', 'N', 'S', 'S'})
	out[4] = version
	out[5] = messageType
	binary.BigEndian.PutUint16(out[6:8], uint16(len(payload)))
	copy(out[8:], payload)
	return out
}

func mustHex(t *testing.T, text string) []byte {
	t.Helper()
	value, err := hex.DecodeString(text)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
