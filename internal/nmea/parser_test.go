package nmea

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sentence(body string) []byte {
	var checksum byte
	for i := 0; i < len(body); i++ {
		checksum ^= body[i]
	}
	return []byte(fmt.Sprintf("$%s*%02X", body, checksum))
}

func TestParseGGAPreservesNoFixRawValues(t *testing.T) {
	got, err := Parse(sentence("GNGGA,123519.00,3201.000,N,11846.000,E,0,00,127.000,45.243,M,0,M,,"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindGGA || got.GGA == nil {
		t.Fatalf("sentence=%+v", got)
	}
	if !got.GGA.Quality.Valid || got.GGA.Quality.Value != 0 {
		t.Fatalf("quality=%+v", got.GGA.Quality)
	}
	if got.GGA.HDOPText != "127.000" {
		t.Fatalf("HDOPText=%q", got.GGA.HDOPText)
	}
	if !got.GGA.GeoidSeparation.Valid || got.GGA.GeoidSeparation.Value != 0 {
		t.Fatalf("geoid separation=%+v", got.GGA.GeoidSeparation)
	}
}

func TestParseGSASystemIDIsOnlyReadFromOptionalField17(t *testing.T) {
	legacyFields := []string{"A", "3", "01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12", "1.0", "0.8", "0.6"}
	modernFields := append(append([]string(nil), legacyFields...), "4")

	tests := []struct {
		name       string
		fields     []string
		wantSystem Field[uint8]
	}{
		{name: "legacy has no system ID", fields: legacyFields},
		{name: "NMEA 4.10 has system ID", fields: modernFields, wantSystem: Field[uint8]{Value: 4, Valid: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(sentence("GNGSA,"+strings.Join(test.fields, ",")), time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if got.GSA == nil || got.GSA.SystemID != test.wantSystem {
				t.Fatalf("GSA=%+v want system=%+v", got.GSA, test.wantSystem)
			}
			if got.GSA.PDOPText != "1.0" || got.GSA.HDOPText != "0.8" || got.GSA.VDOPText != "0.6" {
				t.Fatalf("DOP text not preserved: %+v", got.GSA)
			}
		})
	}
}

func TestParseGSVAcceptsValidZeroSatelliteReport(t *testing.T) {
	got, err := Parse([]byte("$GPGSV,1,1,00*79"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got.GSV == nil || got.GSV.TotalMessages != 1 || got.GSV.MessageNumber != 1 {
		t.Fatalf("GSV=%+v", got.GSV)
	}
	if !got.GSV.VisibleCount.Valid || got.GSV.VisibleCount.Value != 0 {
		t.Fatalf("visible=%+v", got.GSV.VisibleCount)
	}
	if len(got.GSV.Satellites) != 0 {
		t.Fatalf("satellites=%+v", got.GSV.Satellites)
	}
}

func TestParseGSTAllErrorFields(t *testing.T) {
	got, err := Parse(sentence("GNGST,123519.00,0.8,1.1,0.9,45.0,0.5,0.6,0.7"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got.GST == nil {
		t.Fatal("missing GST payload")
	}
	fields := []Field[float64]{
		got.GST.PseudorangeRMS,
		got.GST.SemiMajorError,
		got.GST.SemiMinorError,
		got.GST.OrientationDeg,
		got.GST.LatitudeError,
		got.GST.LongitudeError,
		got.GST.AltitudeError,
	}
	want := []float64{0.8, 1.1, 0.9, 45, 0.5, 0.6, 0.7}
	for i := range want {
		if !fields[i].Valid || fields[i].Value != want[i] {
			t.Fatalf("field %d=%+v want %v", i+1, fields[i], want[i])
		}
	}
}

func TestParseRMCNavigationFields(t *testing.T) {
	receivedAt := time.Date(2026, 8, 3, 10, 11, 12, 13, time.FixedZone("CST", 8*60*60))
	got, err := Parse(sentence("GPRMC,123519.25,A,4807.038,N,01131.000,E,22.4,84.4,230394,,,A"), receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Talker != "GP" || got.Kind != KindRMC || got.RMC == nil || got.ReceivedAt != receivedAt {
		t.Fatalf("sentence=%+v", got)
	}
	if got.RMC.MillisOfDay != 45_319_250 || !got.RMC.TimeValid {
		t.Fatalf("time=%d valid=%t", got.RMC.MillisOfDay, got.RMC.TimeValid)
	}
	if !got.RMC.Date.Valid || !got.RMC.Date.Value.Equal(time.Date(1994, 3, 23, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("date=%+v", got.RMC.Date)
	}
	if !got.RMC.Status.Valid || got.RMC.Status.Value != 'A' {
		t.Fatalf("status=%+v", got.RMC.Status)
	}
	assertFloatField(t, "latitude", got.RMC.Latitude, 48.1173)
	assertFloatField(t, "longitude", got.RMC.Longitude, 11.516666666666667)
	assertFloatField(t, "speed", got.RMC.SpeedKnots, 22.4)
	assertFloatField(t, "course", got.RMC.CourseDeg, 84.4)
}

func TestParseDispatchesSupportedTalkersAndKinds(t *testing.T) {
	tests := []struct {
		body   string
		talker string
		kind   Kind
	}{
		{body: "GPRMC", talker: "GP", kind: KindRMC},
		{body: "BDGGA", talker: "BD", kind: KindGGA},
		{body: "GBGSA", talker: "GB", kind: KindGSA},
		{body: "GNGSV", talker: "GN", kind: KindGSV},
		{body: "GLGST", talker: "GL", kind: KindGST},
		{body: "GAGGA", talker: "GA", kind: KindGGA},
	}
	for _, test := range tests {
		t.Run(test.body, func(t *testing.T) {
			got, err := Parse(sentence(test.body), time.Unix(123, 456))
			if err != nil {
				t.Fatal(err)
			}
			if got.Talker != test.talker || got.Kind != test.kind {
				t.Fatalf("sentence=%+v", got)
			}
			payloads := 0
			for _, present := range []bool{got.RMC != nil, got.GGA != nil, got.GSA != nil, got.GSV != nil, got.GST != nil} {
				if present {
					payloads++
				}
			}
			if payloads != 1 {
				t.Fatalf("payload count=%d sentence=%+v", payloads, got)
			}
		})
	}
}

func TestParseReturnsZeroSentenceForFramingAndIdentifierErrors(t *testing.T) {
	tests := []struct {
		name string
		line []byte
	}{
		{name: "bad checksum", line: []byte("$GPGSV,1,1,00*78")},
		{name: "unsupported type", line: sentence("GPTXT,01,01,02,hello")},
		{name: "short identifier", line: sentence("GPRM")},
		{name: "long identifier", line: sentence("GPRMCX")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.line, time.Now())
			if err == nil {
				t.Fatal("expected error")
			}
			if got != (Sentence{}) {
				t.Fatalf("nonzero sentence on error: %+v", got)
			}
		})
	}
}

func TestParseShortRecognizedSentencesDoesNotPanic(t *testing.T) {
	for _, body := range []string{"GPRMC", "GNGGA,", "GBGSA,A", "GLGSV,1", "GAGST,bad"} {
		t.Run(body, func(t *testing.T) {
			got, err := Parse(sentence(body), time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind == 0 {
				t.Fatalf("missing kind: %+v", got)
			}
		})
	}
}

func TestParseGSVSeparatesSignalIDFromCompleteSatelliteGroups(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantSignal Field[uint8]
	}{
		{name: "complete group without signal", body: "GPGSV,1,1,01,07,79,048,42"},
		{name: "complete group followed by signal", body: "GPGSV,1,1,01,07,79,048,42,1", wantSignal: Field[uint8]{Value: 1, Valid: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(sentence(test.body), time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if got.GSV == nil || len(got.GSV.Satellites) != 1 || got.GSV.Satellites[0].PRN != "07" {
				t.Fatalf("GSV=%+v", got.GSV)
			}
			if got.GSV.SignalID != test.wantSignal {
				t.Fatalf("signal=%+v want=%+v", got.GSV.SignalID, test.wantSignal)
			}
		})
	}
}

func TestParseOptionalNumbersRejectNonFiniteAndPreserveZero(t *testing.T) {
	got, err := Parse(sentence("GNGST,000000.00,0,NaN,+Inf,-Inf,0.0,garbage,0"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got.GST == nil {
		t.Fatal("missing GST")
	}
	for name, field := range map[string]Field[float64]{
		"RMS":      got.GST.PseudorangeRMS,
		"latitude": got.GST.LatitudeError,
		"altitude": got.GST.AltitudeError,
	} {
		if !field.Valid || field.Value != 0 {
			t.Errorf("%s zero=%+v", name, field)
		}
	}
	for name, field := range map[string]Field[float64]{
		"semi-major":  got.GST.SemiMajorError,
		"semi-minor":  got.GST.SemiMinorError,
		"orientation": got.GST.OrientationDeg,
		"longitude":   got.GST.LongitudeError,
	} {
		if field.Valid {
			t.Errorf("%s unexpectedly valid: %+v", name, field)
		}
	}

	gga, err := Parse(sentence("GNGGA,,,,,,256,-1,,,,,,,,"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if gga.GGA.Quality.Valid || gga.GGA.UsedSatellites.Valid {
		t.Fatalf("overflow/negative uint8 accepted: %+v", gga.GGA)
	}
}

func TestParseCoordinatesRejectInvalidValues(t *testing.T) {
	tests := []struct {
		name      string
		latitude  string
		latHem    string
		longitude string
		lonHem    string
		wantLat   bool
		wantLon   bool
	}{
		{name: "valid boundaries", latitude: "9000.000", latHem: "S", longitude: "18000.000", lonHem: "W", wantLat: true, wantLon: true},
		{name: "invalid hemisphere", latitude: "3201.000", latHem: "X", longitude: "11846.000", lonHem: "Q"},
		{name: "minutes at sixty", latitude: "3260.000", latHem: "N", longitude: "11860.000", lonHem: "E"},
		{name: "range exceeded", latitude: "9000.001", latHem: "N", longitude: "18000.001", lonHem: "E"},
		{name: "wrong degree width", latitude: "03201.000", latHem: "N", longitude: "1846.000", lonHem: "E"},
		{name: "one minute digit", latitude: "123.4", latHem: "N", longitude: "1186.0", lonHem: "E"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf("GPRMC,000000.00,A,%s,%s,%s,%s,0,0,010100", test.latitude, test.latHem, test.longitude, test.lonHem)
			got, err := Parse(sentence(body), time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if got.RMC.Latitude.Valid != test.wantLat || got.RMC.Longitude.Valid != test.wantLon {
				t.Fatalf("latitude=%+v longitude=%+v", got.RMC.Latitude, got.RMC.Longitude)
			}
			if test.name == "valid boundaries" && (got.RMC.Latitude.Value != -90 || got.RMC.Longitude.Value != -180) {
				t.Fatalf("boundary values: lat=%+v lon=%+v", got.RMC.Latitude, got.RMC.Longitude)
			}
		})
	}
}

func TestParseTimeAndDateBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		timeText   string
		dateText   string
		wantMillis int64
		wantTime   bool
		wantDate   bool
	}{
		{name: "last millisecond and leap date", timeText: "235959.999", dateText: "290224", wantMillis: 86_399_999, wantTime: true, wantDate: true},
		{name: "midnight", timeText: "000000", dateText: "010100", wantTime: true, wantDate: true},
		{name: "hour 24", timeText: "240000", dateText: "010100", wantDate: true},
		{name: "minute 60", timeText: "126000", dateText: "010100", wantDate: true},
		{name: "second 60", timeText: "125960", dateText: "010100", wantDate: true},
		{name: "bad shape", timeText: "1235", dateText: "010100", wantDate: true},
		{name: "non leap date", timeText: "000000", dateText: "290223", wantTime: true},
		{name: "date wrong width", timeText: "000000", dateText: "10100", wantTime: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(sentence(fmt.Sprintf("GPRMC,%s,A,,,,,,,%s", test.timeText, test.dateText)), time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if got.RMC.TimeValid != test.wantTime || got.RMC.MillisOfDay != test.wantMillis {
				t.Fatalf("time=%d valid=%t", got.RMC.MillisOfDay, got.RMC.TimeValid)
			}
			if got.RMC.Date.Valid != test.wantDate {
				t.Fatalf("date=%+v", got.RMC.Date)
			}
		})
	}
}

func TestParseEveryFixtureLine(t *testing.T) {
	fixtureNames := []string{"multiband-no-fix.nmea", "gps-fix.nmea", "beidou-fix.nmea", "combined-fix.nmea"}
	seenTalkers := make(map[string]bool)
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "testdata", "nmea", name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
				t.Fatal("empty fixture")
			}
			for lineNumber, line := range lines {
				line = strings.TrimSuffix(line, "\r")
				parsed, err := Parse([]byte(line), time.Unix(int64(lineNumber), 0))
				if err != nil {
					t.Fatalf("line %d %q: %v", lineNumber+1, line, err)
				}
				seenTalkers[parsed.Talker] = true
			}
		})
	}
	for _, talker := range []string{"GP", "BD", "GB", "GN", "GL", "GA"} {
		if !seenTalkers[talker] {
			t.Errorf("fixtures do not cover talker %s", talker)
		}
	}
}

func assertFloatField(t *testing.T, name string, got Field[float64], want float64) {
	t.Helper()
	if !got.Valid || math.Abs(got.Value-want) > 1e-12 {
		t.Fatalf("%s=%+v want %.15g", name, got, want)
	}
}
