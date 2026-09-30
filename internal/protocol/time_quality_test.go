package protocol

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"gnssagent/internal/model"
)

func TestEmbeddedTimeQualityGoldenLayouts(t *testing.T) {
	status := model.FullStatus{
		FieldValidityMask: model.FullUTCValid | model.FullRecvValid | model.FullGSTPseudorangeRMSValid,
		UTCTime:           0x0102030405060708, RecvTime: 0x1112131415161718,
		GSTPseudorangeRMS: 0.5,
		TimeQuality: model.TimeQuality{
			Evaluated: true, State: 2, Reason: 2, Samples: 0x0102,
			TimeoutMillis: 0x01020304, RMSValid: true, RMS: 1.5,
		},
	}
	simple := EncodeSimple(status.Simple())
	full := EncodeFull(status)
	if len(simple) != 72 || !bytes.Equal(simple[:8], mustHex(t, "474e535302040040")) {
		t.Fatalf("simple header/size=%x/%d", simple[:8], len(simple))
	}
	if len(full) != 144 || !bytes.Equal(full[:8], mustHex(t, "474e535302030088")) {
		t.Fatalf("full header/size=%x/%d", full[:8], len(full))
	}
	if !bytes.Equal(simple[HeaderSize+58:], mustHex(t, "020201020304")) {
		t.Fatalf("simple quality=%x", simple[HeaderSize+58:])
	}
	if !bytes.Equal(full[HeaderSize+124:], mustHex(t, "02020102010203043fc00000")) {
		t.Fatalf("full quality=%x", full[HeaderSize+124:])
	}
	assertUint64(t, full[HeaderSize:], 0, status.FieldValidityMask|model.FullTimeRMSValid)
	assertFloat32(t, full[HeaderSize:], 96, 0.5)
	assertFloat32(t, full[HeaderSize:], 132, 1.5)
	assertUint64(t, simple[HeaderSize:], 0, model.SimpleUTCValid|model.SimpleRecvValid)
	for _, encoded := range [][]byte{simple, full} {
		frames := NewDecoder(MaxPayload).Feed(encoded)
		if len(frames) != 1 || frames[0].Version != 2 || (frames[0].Type != TypeStatusSimple && frames[0].Type != TypeStatusFull) {
			t.Fatalf("expected one v2 status frame, got %+v", frames)
		}
	}
}

func TestEmbeddedTimeQualitySanitizesMissingAndInvalidFields(t *testing.T) {
	for _, tc := range []struct {
		name     string
		quality  model.TimeQuality
		wantMask uint64
	}{
		{"valid zero RMS", model.TimeQuality{Evaluated: true, RMSValid: true}, model.FullTimeRMSValid},
		{"missing RMS", model.TimeQuality{Evaluated: true, RMS: 2}, 0},
		{"negative RMS", model.TimeQuality{Evaluated: true, RMSValid: true, RMS: -1}, 0},
		{"NaN RMS", model.TimeQuality{Evaluated: true, RMSValid: true, RMS: float32(math.NaN())}, 0},
		{"infinite RMS", model.TimeQuality{Evaluated: true, RMSValid: true, RMS: float32(math.Inf(1))}, 0},
		{"unevaluated", model.TimeQuality{State: 2, Reason: 2, Samples: 10, RMSValid: true, RMS: 1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := model.FullStatus{
				FieldValidityMask: model.FullTimeRMSValid,
				UTCTime:           123, RecvTime: 456, TimeQuality: tc.quality,
			}
			payload := EncodeFull(status)[HeaderSize:]
			assertUint64(t, payload, 0, tc.wantMask)
			if !allZero(payload[8:24]) {
				t.Fatalf("invalid timestamps leaked: %x", payload)
			}
			assertFloat32(t, payload, 132, 0)
			if !tc.quality.Evaluated && (!allZero(payload[124:136]) || !allZero(EncodeSimple(status.Simple())[HeaderSize+58:])) {
				t.Fatalf("unevaluated quality retained values: %x", payload)
			}
		})
	}
}

func TestEmbeddedTimeQualityUnknownEnumsCannotEncodeTrusted(t *testing.T) {
	for _, quality := range []model.TimeQuality{
		{Evaluated: true, State: 255, Reason: 2, Samples: 10},
		{Evaluated: true, State: 2, Reason: 255, Samples: 10},
	} {
		status := model.FullStatus{TimeQuality: quality}
		full := EncodeFull(status)[HeaderSize:]
		simple := EncodeSimple(status.Simple())[HeaderSize:]
		if !allZero(full[124:128]) || !allZero(simple[58:60]) {
			t.Fatalf("invalid enums did not become unknown: full=%x simple=%x", full, simple)
		}
	}
}

func TestEmbeddedTimeQualityCannotEncodeTrustedWithoutValidEvidence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*model.FullStatus)
		wantReason byte
	}{
		{"missing UTC", func(s *model.FullStatus) { s.FieldValidityMask &^= model.FullUTCValid }, 5},
		{"missing receive time", func(s *model.FullStatus) { s.FieldValidityMask &^= model.FullRecvValid }, 5},
		{"missing RMS", func(s *model.FullStatus) { s.TimeQuality.RMSValid = false }, 10},
		{"negative RMS", func(s *model.FullStatus) { s.TimeQuality.RMS = -1 }, 10},
		{"NaN RMS", func(s *model.FullStatus) { s.TimeQuality.RMS = float32(math.NaN()) }, 10},
		{"infinite RMS", func(s *model.FullStatus) { s.TimeQuality.RMS = float32(math.Inf(1)) }, 10},
		{"zero freshness timeout", func(s *model.FullStatus) { s.TimeQuality.TimeoutMillis = 0 }, 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := model.FullStatus{
				FieldValidityMask: model.FullUTCValid | model.FullRecvValid,
				UTCTime:           1000, RecvTime: 1020,
				TimeQuality: model.TimeQuality{
					Evaluated: true, State: 2, Reason: 2, Samples: 10,
					RMSValid: true, RMS: 0, TimeoutMillis: 3000,
				},
			}
			tc.mutate(&status)
			full := EncodeFull(status)[HeaderSize:]
			simple := EncodeSimple(status.Simple())[HeaderSize:]
			if full[124] != 0 || full[125] != tc.wantReason || binary.BigEndian.Uint16(full[126:128]) != 0 {
				t.Fatalf("invalid FULL trust=%x want Unknown/reason=%d/samples=0", full[124:], tc.wantReason)
			}
			if simple[58] != 0 || simple[59] != tc.wantReason {
				t.Fatalf("invalid SIMPLE trust=%x want Unknown/reason=%d", simple[58:], tc.wantReason)
			}
		})
	}
}

func TestDecoderRejectsV1StatusLengthsForV2Frames(t *testing.T) {
	for _, tc := range []struct {
		kind      byte
		oldLength int
	}{
		{TypeStatusSimple, 58}, {TypeStatusFull, 124},
	} {
		input := rawFrame(Version, tc.kind, make([]byte, tc.oldLength))
		input = append(input, EncodeSubscribeRequest(StatusSimple)...)
		frames := NewDecoder(MaxPayload).Feed(input)
		if len(frames) != 1 || frames[0].Type != TypeSubscribeRequest {
			t.Fatalf("type=%x frames=%+v", tc.kind, frames)
		}
	}
}
