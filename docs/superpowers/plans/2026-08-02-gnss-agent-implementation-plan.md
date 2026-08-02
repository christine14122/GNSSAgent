# GNSSAgent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a production-ready Go service that exclusively reads a GNSS UART, parses and aggregates NMEA once per UTC second, publishes SIMPLE/FULL custom binary status frames, and executes loopback-only GNSS control requests.

**Architecture:** A single process composes focused packages for configuration, NMEA framing/parsing, cycle aggregation, wire encoding, Linux serial access, control, and TCP sessions. Parsed GNSS state is built once per cycle; each subscriber receives a connection-specific SIMPLE or FULL encoding through a latest-value queue. Device-specific defaults are selected by link-time target metadata while protocol and parser behavior remain shared.

**Tech Stack:** Go 1.23 language baseline; Go 1.25.5 and 1.23.12 build toolchains; standard library; `golang.org/x/sys/unix v0.30.0`; Linux termios/ioctl; TCP; SysV init; PowerShell build scripts.

---

## Scope and execution rules

- Treat [`2026-08-02-gnss-agent-requirements-design.md`](../specs/2026-08-02-gnss-agent-requirements-design.md) as the internal behavior specification.
- Treat [`GNSSAgent-Binary-Protocol-v1.md`](../../protocol/GNSSAgent-Binary-Protocol-v1.md) as the byte-for-byte external contract.
- Use TDD for every package: add a focused failing test, run it, implement the smallest behavior, run the focused test, then run the package suite.
- Do not access or stop `copy_RadioApp` during host-side development. Device tests that require `/dev/ttyUL4` occur only after the modified `copy_RadioApp` has released it.
- Do not add Protobuf, application CRC, heartbeats, per-satellite wire fields, system clock modification, or a second control connection.
- Preserve the v1 wire sizes: SIMPLE 58-byte payload/66-byte frame; FULL 124-byte payload/132-byte frame.

## Planned file structure

```text
go.mod
go.sum
.gitignore
cmd/gnssagent/main.go
internal/buildinfo/buildinfo.go
internal/config/config.go
internal/config/config_test.go
internal/model/status.go
internal/model/status_test.go
internal/nmea/types.go
internal/nmea/framer.go
internal/nmea/framer_test.go
internal/nmea/decimal.go
internal/nmea/decimal_test.go
internal/nmea/parser.go
internal/nmea/rmc.go
internal/nmea/gga.go
internal/nmea/gsa.go
internal/nmea/gsv.go
internal/nmea/gst.go
internal/nmea/parser_test.go
internal/aggregate/aggregator.go
internal/aggregate/cycle.go
internal/aggregate/satellites.go
internal/aggregate/dop.go
internal/aggregate/aggregator_test.go
internal/aggregate/satellites_test.go
internal/protocol/header.go
internal/protocol/messages.go
internal/protocol/status.go
internal/protocol/decoder.go
internal/protocol/protocol_test.go
internal/serial/port.go
internal/serial/port_linux.go
internal/serial/port_unsupported.go
internal/serial/icount_linux.go
internal/serial/endpoint.go
internal/serial/endpoint_test.go
internal/serial/metrics.go
internal/serial/metrics_test.go
internal/control/controller.go
internal/control/controller_test.go
internal/server/server.go
internal/server/session.go
internal/server/limits.go
internal/server/server_test.go
internal/app/app.go
internal/app/app_test.go
internal/observe/stats.go
internal/observe/stats_test.go
testdata/nmea/multiband-no-fix.nmea
testdata/nmea/gps-fix.nmea
testdata/nmea/beidou-fix.nmea
testdata/nmea/combined-fix.nmea
build/scripts/build.ps1
build/scripts/build-hf.ps1
build/scripts/build.bat
tests/build.test.ps1
tests/protocol_doc.test.ps1
deploy/default/gnssagent
deploy/init.d/gnssagent
tests/device/smoke.sh
tests/device/capture-link-budget.sh
```

Each file has one primary responsibility. In particular, NMEA sentence parsing does not know the wire protocol, and the protocol package does not know serial or NMEA details.

### Task 1: Initialize repository, module, build metadata, and configuration

**Files:**
- Create: `.gitignore`
- Create: `go.mod`
- Create: `go.sum`
- Create: `internal/buildinfo/buildinfo.go`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Initialize Git and preserve the approved documents as the first commit**

Run:

```powershell
git init
git add docs
git commit -m "docs: define GNSSAgent requirements and protocol"
```

Expected: a new repository with one root commit containing both approved documents and this plan.

- [ ] **Step 2: Create module metadata and ignore generated artifacts**

Create `go.mod`:

```go
module gnssagent

go 1.23.0

require golang.org/x/sys v0.30.0
```

Create `.gitignore`:

```gitignore
/build/dist/
/build/compiler/.go*/
*.test
*.out
coverage.txt
```

Run:

```powershell
go mod download golang.org/x/sys@v0.30.0
```

Expected: `go.sum` contains checksums for `golang.org/x/sys v0.30.0`; the explicit requirement remains in `go.mod` until the Linux serial package starts using it.

- [ ] **Step 3: Write failing configuration tests**

Create `internal/config/config_test.go`:

```go
package config

import "testing"

func TestParseMultibandDefaults(t *testing.T) {
	cfg, err := Parse(nil, "multiband-radio")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SerialDevice != "/dev/ttyUL4" || cfg.Baud != 9600 {
		t.Fatalf("unexpected serial defaults: %+v", cfg)
	}
	if cfg.ListenAddress != "0.0.0.0:29501" {
		t.Fatalf("unexpected listen address: %s", cfg.ListenAddress)
	}
	if cfg.MaxConnections != 5 || cfg.MaxRemoteConnections != 4 {
		t.Fatalf("unexpected limits: %+v", cfg)
	}
}

func TestParseUnknownTargetRequiresSerial(t *testing.T) {
	_, err := Parse(nil, "hf")
	if err == nil {
		t.Fatal("expected missing serial device error")
	}
}

func TestParseOverridesUnknownTarget(t *testing.T) {
	cfg, err := Parse([]string{"--serial", "/dev/ttyS2", "--baud", "19200"}, "hf")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SerialDevice != "/dev/ttyS2" || cfg.Baud != 19200 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestParseRejectsLimitsWithoutLoopbackReservation(t *testing.T) {
	_, err := Parse([]string{"--max-connections", "5", "--max-remote-connections", "5"}, "multiband-radio")
	if err == nil {
		t.Fatal("expected remote limit validation error")
	}
}
```

- [ ] **Step 4: Run the configuration tests and verify the expected failure**

Run:

```powershell
go test ./internal/config -run TestParse -v
```

Expected: FAIL because `Parse` and `Config` do not exist.

- [ ] **Step 5: Implement build metadata and configuration parsing**

Create `internal/buildinfo/buildinfo.go`:

```go
package buildinfo

var Target = "generic"
```

Create `internal/config/config.go`:

```go
package config

import (
	"errors"
	"flag"
	"fmt"
)

type Config struct {
	SerialDevice         string
	Baud                 int
	ListenAddress        string
	MaxConnections       int
	MaxRemoteConnections int
	LogLevel             string
}

func Parse(args []string, target string) (Config, error) {
	cfg := Config{
		Baud:                 9600,
		ListenAddress:        "0.0.0.0:29501",
		MaxConnections:       5,
		MaxRemoteConnections: 4,
		LogLevel:             "info",
	}
	if target == "multiband-radio" {
		cfg.SerialDevice = "/dev/ttyUL4"
	}

	fs := flag.NewFlagSet("gnssagent", flag.ContinueOnError)
	fs.StringVar(&cfg.SerialDevice, "serial", cfg.SerialDevice, "GNSS UART device")
	fs.IntVar(&cfg.Baud, "baud", cfg.Baud, "GNSS UART baud")
	fs.StringVar(&cfg.ListenAddress, "listen", cfg.ListenAddress, "TCP listen address")
	fs.IntVar(&cfg.MaxConnections, "max-connections", cfg.MaxConnections, "maximum total TCP connections")
	fs.IntVar(&cfg.MaxRemoteConnections, "max-remote-connections", cfg.MaxRemoteConnections, "maximum non-loopback TCP connections")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "debug, info, warn, or error")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if cfg.SerialDevice == "" {
		return Config{}, errors.New("serial device is required for this target")
	}
	if cfg.Baud <= 0 {
		return Config{}, fmt.Errorf("baud must be positive: %d", cfg.Baud)
	}
	if cfg.MaxConnections < 1 {
		return Config{}, errors.New("max-connections must be positive")
	}
	if cfg.MaxRemoteConnections < 0 || cfg.MaxRemoteConnections >= cfg.MaxConnections {
		return Config{}, errors.New("max-remote-connections must leave at least one loopback slot")
	}
	return cfg, nil
}
```

- [ ] **Step 6: Run tests, format, vet, and commit**

Run:

```powershell
gofmt -w internal/buildinfo/buildinfo.go internal/config/config.go internal/config/config_test.go
go test ./internal/config -v
go vet ./internal/config
git add .gitignore go.mod go.sum internal/buildinfo internal/config
git commit -m "feat: add target-aware GNSSAgent configuration"
```

Expected: tests and vet PASS; commit succeeds.

### Task 2: Define the internal status model and validity masks

**Files:**
- Create: `internal/model/status.go`
- Test: `internal/model/status_test.go`

- [ ] **Step 1: Write failing tests for FULL-to-SIMPLE projection**

Create `internal/model/status_test.go`:

```go
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
```

- [ ] **Step 2: Run the model tests and verify failure**

Run:

```powershell
go test ./internal/model -v
```

Expected: FAIL because the status types and mask constants do not exist.

- [ ] **Step 3: Implement exact FULL/SIMPLE data types and masks**

Create `internal/model/status.go` with the exact field order from the protocol:

```go
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
```

- [ ] **Step 4: Run model tests and commit**

Run:

```powershell
gofmt -w internal/model/status.go internal/model/status_test.go
go test ./internal/model -v
git add internal/model
git commit -m "feat: define GNSS status model and validity masks"
```

Expected: PASS; the FULL mask ends at bit 28 and SIMPLE ends at bit 8.

### Task 3: Implement NMEA framing, checksum validation, and decimal helpers

**Files:**
- Create: `internal/nmea/types.go`
- Create: `internal/nmea/framer.go`
- Create: `internal/nmea/decimal.go`
- Test: `internal/nmea/framer_test.go`
- Test: `internal/nmea/decimal_test.go`

- [ ] **Step 1: Write failing framer and checksum tests**

Create `internal/nmea/framer_test.go`:

```go
package nmea

import "testing"

func TestFramerHandlesFragmentsNoiseAndMultipleLines(t *testing.T) {
	f := NewFramer(1024)
	if got := f.Feed([]byte("noise$GNRM")); len(got) != 0 {
		t.Fatalf("unexpected frame: %q", got)
	}
	got := f.Feed([]byte("C,1*00\r\n$GPGSV,1,1,00*79\n"))
	if len(got) != 2 || string(got[1]) != "$GPGSV,1,1,00*79" {
		t.Fatalf("frames=%q", got)
	}
}

func TestFramerDropsOverlongLineAndResynchronizes(t *testing.T) {
	f := NewFramer(16)
	got := f.Feed([]byte("$12345678901234567$X*00\n"))
	if len(got) != 1 || string(got[0]) != "$X*00" {
		t.Fatalf("frames=%q", got)
	}
}

func TestValidateChecksum(t *testing.T) {
	if err := ValidateChecksum([]byte("$GPGSV,1,1,00*79")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateChecksum([]byte("$GPGSV,1,1,00*78")); err == nil {
		t.Fatal("expected checksum error")
	}
}
```

- [ ] **Step 2: Write failing lexical decimal tests including the reviewed boundary**

Create `internal/nmea/decimal_test.go`:

```go
package nmea

import "testing"

func TestParseMilliDecimalHalfUpWithoutFloat(t *testing.T) {
	tests := map[string]int64{
		"1":       1000,
		"1.2":     1200,
		"1.234":   1234,
		"1.2344":  1234,
		"1.2345":  1235,
		"0.4895":  490,
		"0.5005":  501,
		"127.000": 127000,
	}
	for input, want := range tests {
		got, err := ParseMilliDecimal(input)
		if err != nil || got != want {
			t.Fatalf("%s: got=%d err=%v want=%d", input, got, err, want)
		}
	}
}

func TestReviewedDOPBoundaryIsElevenMilli(t *testing.T) {
	a, _ := ParseMilliDecimal("0.4895")
	b, _ := ParseMilliDecimal("0.5005")
	if b-a != 11 {
		t.Fatalf("difference=%d want=11", b-a)
	}
}
```

- [ ] **Step 3: Run focused tests and verify failure**

Run:

```powershell
go test ./internal/nmea -run "TestFramer|TestValidateChecksum|TestParseMilli|TestReviewed" -v
```

Expected: FAIL because `Framer`, `ValidateChecksum`, and `ParseMilliDecimal` do not exist.

- [ ] **Step 4: Implement common field types, the framer, checksum, and lexical conversion**

Create `internal/nmea/types.go`:

```go
package nmea

type Field[T any] struct {
	Value T
	Valid bool
}

type Satellite struct {
	PRN       string
	Elevation Field[float32]
	Azimuth   Field[float32]
	CN0       Field[float32]
}
```

Create `internal/nmea/framer.go`:

```go
package nmea

import (
	"bytes"
	"errors"
	"fmt"
)

type Framer struct {
	max        int
	buf        []byte
	discarding bool
}

func NewFramer(max int) *Framer { return &Framer{max: max} }

func (f *Framer) Feed(input []byte) [][]byte {
	var out [][]byte
	for _, b := range input {
		if b == '$' {
			f.buf = []byte{'$'}
			f.discarding = false
			continue
		}
		if len(f.buf) == 0 || f.discarding {
			continue
		}
		if b == '\n' {
			line := bytes.TrimSuffix(f.buf, []byte{'\r'})
			out = append(out, append([]byte(nil), line...))
			f.buf = nil
			continue
		}
		f.buf = append(f.buf, b)
		if len(f.buf) > f.max {
			f.buf = nil
			f.discarding = true
		}
	}
	return out
}

func ValidateChecksum(line []byte) error {
	if len(line) < 5 || line[0] != '$' {
		return errors.New("invalid NMEA framing")
	}
	star := bytes.LastIndexByte(line, '*')
	if star < 2 || star+3 != len(line) {
		return errors.New("missing NMEA checksum")
	}
	var sum byte
	for _, b := range line[1:star] {
		sum ^= b
	}
	var want byte
	if _, err := fmt.Sscanf(string(line[star+1:]), "%02X", &want); err != nil {
		return errors.New("invalid NMEA checksum text")
	}
	if sum != want {
		return fmt.Errorf("NMEA checksum mismatch: got %02X want %02X", sum, want)
	}
	return nil
}
```

Create `internal/nmea/decimal.go` using digit operations only:

```go
package nmea

import (
	"errors"
	"strings"
)

func ParseMilliDecimal(text string) (int64, error) {
	if text == "" || strings.HasPrefix(text, "-") || strings.Count(text, ".") > 1 {
		return 0, errors.New("invalid non-negative decimal")
	}
	parts := strings.SplitN(text, ".", 2)
	whole, err := decimalDigits(parts[0])
	if err != nil {
		return 0, err
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	for len(fraction) < 4 {
		fraction += "0"
	}
	firstThree, err := decimalDigits(fraction[:3])
	if err != nil {
		return 0, err
	}
	for _, b := range []byte(fraction[3:]) {
		if b < '0' || b > '9' {
			return 0, errors.New("invalid decimal digit")
		}
	}
	if fraction[3] >= '5' {
		firstThree++
	}
	return whole*1000 + firstThree, nil
}

func decimalDigits(text string) (int64, error) {
	if text == "" {
		return 0, errors.New("missing decimal digits")
	}
	var value int64
	for _, b := range []byte(text) {
		if b < '0' || b > '9' {
			return 0, errors.New("invalid decimal digit")
		}
		value = value*10 + int64(b-'0')
	}
	return value, nil
}
```

- [ ] **Step 5: Run tests and commit**

Run:

```powershell
gofmt -w internal/nmea
go test ./internal/nmea -v
git add internal/nmea
git commit -m "feat: frame and validate NMEA input"
```

Expected: all framing, checksum, and lexical decimal tests PASS.

### Task 4: Parse RMC, GGA, GSA, GSV, and GST sentences

**Files:**
- Modify: `internal/nmea/types.go`
- Create: `internal/nmea/parser.go`
- Create: `internal/nmea/rmc.go`
- Create: `internal/nmea/gga.go`
- Create: `internal/nmea/gsa.go`
- Create: `internal/nmea/gsv.go`
- Create: `internal/nmea/gst.go`
- Test: `internal/nmea/parser_test.go`
- Create: `testdata/nmea/multiband-no-fix.nmea`
- Create: `testdata/nmea/gps-fix.nmea`
- Create: `testdata/nmea/beidou-fix.nmea`
- Create: `testdata/nmea/combined-fix.nmea`

- [ ] **Step 1: Define typed parser output and write failing table tests**

Extend `internal/nmea/types.go` with these public parser contracts:

```go
type Kind uint8

const (
	KindRMC Kind = iota + 1
	KindGGA
	KindGSA
	KindGSV
	KindGST
)

type Sentence struct {
	Talker     string
	Kind       Kind
	ReceivedAt time.Time
	RMC        *RMC
	GGA        *GGA
	GSA        *GSA
	GSV        *GSV
	GST        *GST
}

type RMC struct {
	MillisOfDay int64
	TimeValid   bool
	Date        Field[time.Time]
	Status      Field[byte]
	Latitude    Field[float64]
	Longitude   Field[float64]
	SpeedKnots  Field[float64]
	CourseDeg   Field[float64]
}

type GGA struct {
	MillisOfDay       int64
	TimeValid         bool
	Latitude          Field[float64]
	Longitude         Field[float64]
	Quality           Field[uint8]
	UsedSatellites    Field[uint8]
	HDOPText          string
	AltitudeMSL       Field[float64]
	GeoidSeparation   Field[float64]
	DifferentialAge   Field[float64]
}

type GSA struct {
	FixDimension Field[uint8]
	PRNs         []string
	PDOPText     string
	HDOPText     string
	VDOPText     string
	SystemID     Field[uint8]
}

type GSV struct {
	TotalMessages int
	MessageNumber int
	VisibleCount  Field[uint8]
	Satellites    []Satellite
	SignalID      Field[uint8]
}

type GST struct {
	MillisOfDay     int64
	TimeValid       bool
	PseudorangeRMS  Field[float64]
	SemiMajorError  Field[float64]
	SemiMinorError  Field[float64]
	OrientationDeg Field[float64]
	LatitudeError  Field[float64]
	LongitudeError Field[float64]
	AltitudeError  Field[float64]
}
```

Add imports `time` to `types.go`. Create `internal/nmea/parser_test.go` with a checksum helper so test bodies remain readable:

```go
package nmea

import (
	"fmt"
	"testing"
	"time"
)

func sentence(body string) []byte {
	var sum byte
	for _, b := range []byte(body) {
		sum ^= b
	}
	return []byte(fmt.Sprintf("$%s*%02X", body, sum))
}

func TestParseNoFixSentinelsRemainRawForAggregator(t *testing.T) {
	got, err := Parse(sentence("GNGGA,123519.00,3201.000,N,11846.000,E,0,00,127.000,45.243,M,0,M,,"), time.UnixMilli(100))
	if err != nil {
		t.Fatal(err)
	}
	if got.GGA.Quality.Value != 0 || got.GGA.HDOPText != "127.000" {
		t.Fatalf("unexpected GGA: %+v", got.GGA)
	}
	if !got.GGA.GeoidSeparation.Valid || got.GGA.GeoidSeparation.Value != 0 {
		t.Fatalf("geoid field should be parseable raw input: %+v", got.GGA.GeoidSeparation)
	}
}

func TestParseGSAWithAndWithoutSystemID(t *testing.T) {
	without, err := Parse(sentence("GNGSA,A,1,,,,,,,,,,,,,127.000,127.000,127.000"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if without.GSA.SystemID.Valid {
		t.Fatal("legacy 17-field GSA must not invent System ID")
	}
	with, err := Parse(sentence("GNGSA,A,3,01,05,,,,,,,,,,,1.23,0.98,0.75,1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !with.GSA.SystemID.Valid || with.GSA.SystemID.Value != 1 {
		t.Fatalf("unexpected System ID: %+v", with.GSA.SystemID)
	}
}

func TestParseZeroSatelliteGSVIsCompleteAndValid(t *testing.T) {
	got, err := Parse([]byte("$GPGSV,1,1,00*79"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got.GSV.TotalMessages != 1 || got.GSV.MessageNumber != 1 ||
		!got.GSV.VisibleCount.Valid || got.GSV.VisibleCount.Value != 0 {
		t.Fatalf("unexpected GSV: %+v", got.GSV)
	}
}

func TestParseGSTSevenFields(t *testing.T) {
	got, err := Parse(sentence("GNGST,123519.00,0.8,0.5,0.3,45.0,0.4,0.3,0.7"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got.GST.PseudorangeRMS.Value != 0.8 || got.GST.AltitudeError.Value != 0.7 {
		t.Fatalf("unexpected GST: %+v", got.GST)
	}
}
```

- [ ] **Step 2: Run parser tests and verify failure**

Run:

```powershell
go test ./internal/nmea -run "TestParse" -v
```

Expected: FAIL because the sentence types and `Parse` do not exist.

- [ ] **Step 3: Implement checksum-first dispatch and exact sentence indexes**

Create `internal/nmea/parser.go`:

```go
package nmea

import (
	"bytes"
	"errors"
	"strings"
	"time"
)

func Parse(line []byte, receivedAt time.Time) (Sentence, error) {
	if err := ValidateChecksum(line); err != nil {
		return Sentence{}, err
	}
	star := bytes.LastIndexByte(line, '*')
	fields := strings.Split(string(line[1:star]), ",")
	if len(fields) == 0 || len(fields[0]) != 5 {
		return Sentence{}, errors.New("invalid NMEA sentence identifier")
	}
	out := Sentence{Talker: fields[0][:2], ReceivedAt: receivedAt}
	switch fields[0][2:] {
	case "RMC":
		out.Kind, out.RMC = KindRMC, parseRMC(fields[1:])
	case "GGA":
		out.Kind, out.GGA = KindGGA, parseGGA(fields[1:])
	case "GSA":
		out.Kind, out.GSA = KindGSA, parseGSA(fields[1:])
	case "GSV":
		out.Kind, out.GSV = KindGSV, parseGSV(fields[1:])
	case "GST":
		out.Kind, out.GST = KindGST, parseGST(fields[1:])
	default:
		return Sentence{}, errors.New("unsupported NMEA sentence")
	}
	return out, nil
}
```

Implement the five helpers in their named files with these exact source indexes:

| Helper | Required field mapping |
|---|---|
| `parseRMC` | 0 time, 1 A/V status, 2–5 coordinate/hemisphere, 6 knots, 7 course, 8 DDMMYY date |
| `parseGGA` | 0 time, 1–4 coordinate/hemisphere, 5 quality, 6 used count, 7 HDOP raw text, 8 MSL altitude, 10 geoid separation, 12 differential age |
| `parseGSA` | 1 fix dimension, 2–13 PRN slots, 14/15/16 DOP raw text, optional 17 System ID |
| `parseGSV` | 0 total packets, 1 packet number, 2 visible count, repeated groups of four from 3; optional final Signal ID after complete groups |
| `parseGST` | 0 time and 1–7 in protocol order |

Use these conversion rules:

```go
func parseOptionalFloat(text string) Field[float64] {
	value, err := strconv.ParseFloat(text, 64)
	return Field[float64]{Value: value, Valid: err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)}
}

func parseCoordinate(text, hemisphere string, degreeDigits int) Field[float64] {
	if len(text) <= degreeDigits {
		return Field[float64]{}
	}
	degrees, err1 := strconv.ParseFloat(text[:degreeDigits], 64)
	minutes, err2 := strconv.ParseFloat(text[degreeDigits:], 64)
	if err1 != nil || err2 != nil || minutes < 0 || minutes >= 60 {
		return Field[float64]{}
	}
	value := degrees + minutes/60
	if hemisphere == "S" || hemisphere == "W" {
		value = -value
	} else if hemisphere != "N" && hemisphere != "E" {
		return Field[float64]{}
	}
	return Field[float64]{Value: value, Valid: true}
}
```

Parse time-of-day to integer milliseconds without attaching a date. Parse RMC date only when all six digits exist; combine it with time in the aggregator. Keep DOP as raw decimal text so the reviewed lexical conversion remains possible.

- [ ] **Step 4: Add representative captured and synthetic fixtures**

Populate the four files under `testdata/nmea/` with checksum-correct lines. `multiband-no-fix.nmea` must contain the observed shape:

```text
$GNRMC,123519.00,V,3201.000,N,11846.000,E,0.000,0.000,,,E,N*1A
$GNGGA,123519.00,3201.000,N,11846.000,E,0,00,127.000,45.243,M,0,M,,*6A
$GNGSA,A,1,,,,,,,,,,,,,127.000,127.000,127.000*2A
$GNGSA,A,1,,,,,,,,,,,,,127.000,127.000,127.000*2A
$GPGSV,1,1,00*79
```

Make the test load every fixture line through `Parse`. The three fix fixtures must contain checksum-correct date-bearing RMC, quality-positive GGA, populated GSA PRNs, complete multi-packet GSV, and one synthetic GST sentence; generate each checksum with the same XOR helper used in `parser_test.go` before committing the fixture.

- [ ] **Step 5: Run all NMEA tests and commit**

Run:

```powershell
gofmt -w internal/nmea
go test ./internal/nmea -v
git add internal/nmea testdata/nmea
git commit -m "feat: parse GNSS NMEA sentences"
```

Expected: parser tests PASS for GP, BD/GB, GN, GL, and GA talkers; invalid checksums and unsupported sentence types return errors without state mutation.

### Task 5: Aggregate one status per UTC cycle

**Files:**
- Create: `internal/aggregate/aggregator.go`
- Create: `internal/aggregate/cycle.go`
- Create: `internal/aggregate/satellites.go`
- Create: `internal/aggregate/dop.go`
- Test: `internal/aggregate/aggregator_test.go`
- Test: `internal/aggregate/satellites_test.go`

- [ ] **Step 1: Write failing cycle and no-stale-carry tests**

Create `internal/aggregate/aggregator_test.go` with a test builder that calls the real NMEA parser. Include these assertions:

```go
func TestNextUTCSecondFinalizesPreviousCycle(t *testing.T) {
	a := New()
	first := mustSentence(t, "GNRMC,123519.00,A,3201.000,N,11846.000,E,1.0,90.0,020826,,,A")
	second := mustSentence(t, "GNRMC,123520.00,A,3201.100,N,11846.100,E,2.0,91.0,020826,,,A")
	if _, ok := a.Add(first); ok {
		t.Fatal("first sentence must not publish immediately")
	}
	status, ok := a.Add(second)
	if !ok || status.UTCTime == 0 {
		t.Fatalf("expected finalized status: %+v ok=%v", status, ok)
	}
}

func TestFlushAfterOnePointFiveSeconds(t *testing.T) {
	a := New()
	s := mustSentenceAt(t, "GNGGA,123519.00,3201.000,N,11846.000,E,1,08,0.9,45.0,M,-10.0,M,,", time.UnixMilli(1000))
	a.Add(s)
	if _, ok := a.FlushExpired(time.UnixMilli(2499)); ok {
		t.Fatal("must not flush before 1.5 seconds")
	}
	if _, ok := a.FlushExpired(time.UnixMilli(2500)); !ok {
		t.Fatal("expected timeout flush")
	}
}

func TestMissingFieldDoesNotCarryFromPreviousCycle(t *testing.T) {
	a := New()
	a.Add(mustSentence(t, "GNGGA,123519.00,3201.000,N,11846.000,E,1,08,0.9,45.0,M,-10.0,M,,"))
	a.Add(mustSentence(t, "GNRMC,123520.00,A,3201.000,N,11846.000,E,0.0,0.0,020826,,,A"))
	status, _ := a.FlushExpired(time.Now().Add(2 * time.Second))
	if status.FieldValidityMask&model.FullGGAHDOPValid != 0 {
		t.Fatal("GGA HDOP leaked into next cycle")
	}
}
```

- [ ] **Step 2: Write failing validity, DOP, height, and satellite identity tests**

Add tests that prove:

```go
func TestNoFixSentinelsAreInvalid(t *testing.T) {
	status := aggregateBodies(t,
		"GNGGA,123519.00,3201.000,N,11846.000,E,0,00,127.000,45.243,M,0,M,,",
		"GNGSA,A,1,,,,,,,,,,,,,127.000,127.000,127.000",
	)
	for _, bit := range []uint64{
		model.FullGGAHDOPValid,
		model.FullGSAPDOPValid,
		model.FullGSAHDOPValid,
		model.FullGSAVDOPValid,
		model.FullAltitudeEllipsoidValid,
	} {
		if status.FieldValidityMask&bit != 0 {
			t.Fatalf("unexpected valid bit %#x in %#x", bit, status.FieldValidityMask)
		}
	}
}

func TestDOPLexicalBoundaryConflicts(t *testing.T) {
	status := aggregateBodies(t,
		"GNGSA,A,3,01,,,,,,,,,,,,0.4895,1.000,1.000",
		"GNGSA,A,3,02,,,,,,,,,,,,0.5005,1.000,1.000",
	)
	if status.FieldValidityMask&model.FullGSAPDOPValid != 0 {
		t.Fatal("11 milli-DOP difference must invalidate the GSA DOP group")
	}
}

func TestDOPWithinTenMilliUsesFirstGroup(t *testing.T) {
	status := aggregateBodies(t,
		"GNGSA,A,3,01,,,,,,,,,,,,1.230,0.980,0.750",
		"GNGSA,A,3,02,,,,,,,,,,,,1.240,0.990,0.760",
	)
	if status.GSAPDOP != 1.23 || status.GSAHDOP != 0.98 || status.GSAVDOP != 0.75 {
		t.Fatalf("must publish first DOP group: %+v", status)
	}
}
```

In `satellites_test.go`, cover all identity priorities and mixed ambiguity:

```go
func TestUnknownGNPRNCollisionInvalidatesGSAUsedFallback(t *testing.T) {
	result := countUsed(nil, []GSAIdentity{
		{Talker: "GN", PRNs: []string{"05"}},
		{Talker: "GN", PRNs: []string{"05"}},
	})
	if result.Valid {
		t.Fatal("unresolved repeated PRN must be ambiguous")
	}
}

func TestUnknownPRNCollidingWithKnownIdentityIsAmbiguous(t *testing.T) {
	result := countUsed(nil, []GSAIdentity{
		{Talker: "GP", PRNs: []string{"05"}},
		{Talker: "GN", PRNs: []string{"05"}},
	})
	if result.Valid {
		t.Fatal("mixed known and unknown raw PRN collision must be ambiguous")
	}
}
```

Also test GGA count precedence, complete `GPGSV,1,1,00` as valid zero, incomplete GSV invalid, unique GSV correlation, average used C/N0, and four constellation counts.

- [ ] **Step 3: Run aggregate tests and verify failure**

Run:

```powershell
go test ./internal/aggregate -v
```

Expected: FAIL because the aggregate package does not exist.

- [ ] **Step 4: Implement the aggregator public API and cycle state**

Create `internal/aggregate/aggregator.go`:

```go
package aggregate

import (
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
)

const flushDelay = 1500 * time.Millisecond

type Aggregator struct {
	current *cycle
}

func New() *Aggregator { return &Aggregator{} }

func (a *Aggregator) Add(s nmea.Sentence) (model.FullStatus, bool) {
	key, timed := sentenceSecond(s)
	if a.current == nil {
		a.current = newCycle(key, timed, s.ReceivedAt)
	} else if timed && !a.current.timed {
		a.current.key = key
		a.current.timed = true
	} else if timed && a.current.timed && key != a.current.key {
		out := a.current.status()
		a.current = newCycle(key, true, s.ReceivedAt)
		a.current.add(s)
		return out, true
	}
	a.current.add(s)
	return model.FullStatus{}, false
}

func (a *Aggregator) FlushExpired(now time.Time) (model.FullStatus, bool) {
	if a.current == nil || now.Sub(a.current.firstReceivedAt) < flushDelay {
		return model.FullStatus{}, false
	}
	out := a.current.status()
	a.current = nil
	return out, true
}

func (a *Aggregator) Clear() { a.current = nil }
```

Implement `cycle.go` so a fresh cycle owns all RMC/GGA/GSA/GSV/GST state and builds a zero-initialized `model.FullStatus` on every `status()` call. Set `RecvTime` from the cycle's first checksum-correct sentence. Convert RMC date plus time to Unix milliseconds; do not synthesize a date. Apply the RMC/GGA valid truth table exactly as specified.

Map every business field explicitly:

- prefer GGA latitude/longitude and fall back to RMC;
- set MSL altitude from parseable GGA altitude, but set ellipsoid altitude only for quality >0 with both MSL and geoid values;
- use maximum parseable GSA fix dimension and raw GGA quality as solution type;
- prefer GGA used count and use the GSA ambiguity rules only as fallback;
- require complete GSV cycles for constellation counts and used C/N0 correlation;
- keep GGA HDOP independent from all GSA DOP values;
- accept finite non-negative differential age including zero;
- convert RMC knots with `metersPerSecond = knots * 0.5144444444444445`;
- copy course in degrees and the seven GST values without deriving horizontal/vertical accuracy;
- leave every invalid value at zero and set only its matching mask bit.

- [ ] **Step 5: Implement deterministic DOP and satellite helpers**

In `dop.go`, retain both raw text and parsed float. Use `nmea.ParseMilliDecimal` for comparisons and 127000 sentinel recognition. Accept one complete valid group; for multiple groups require each component's max-minus-min milli value to be at most 10 and publish the first group.

In `satellites.go`, define:

```go
type SatelliteKey struct {
	Constellation string
	PRN           string
}

type GSAIdentity struct {
	Talker  string
	SystemID nmea.Field[uint8]
	PRNs    []string
}

type CountResult struct {
	Value uint8
	Valid bool
}
```

Resolve identity in this exact order: System ID, specific talker, unique match in a complete GSV set, then target-confirmed PRN mapping. When GGA count exists, bypass GSA ambiguity. Without GGA, invalidate the entire fallback if an unresolved PRN repeats across GSA sentences or collides with the raw PRN of a resolved identity.

- [ ] **Step 6: Run aggregate tests, race tests, and commit**

Run:

```powershell
gofmt -w internal/aggregate
go test ./internal/aggregate -v
go test -race ./internal/aggregate
git add internal/aggregate
git commit -m "feat: aggregate GNSS state by UTC cycle"
```

Expected: all cycle, validity, DOP, height, GSV, identity, and no-stale-carry tests PASS.

### Task 6: Implement the exact v1 binary protocol

**Files:**
- Create: `internal/protocol/header.go`
- Create: `internal/protocol/messages.go`
- Create: `internal/protocol/status.go`
- Create: `internal/protocol/decoder.go`
- Test: `internal/protocol/protocol_test.go`

- [ ] **Step 1: Write failing constants, status size, and golden frame tests**

Create `internal/protocol/protocol_test.go`:

```go
package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"

	"gnssagent/internal/model"
)

func TestFixedFrameSizes(t *testing.T) {
	if got := len(EncodeSimple(model.SimpleStatus{})); got != 66 {
		t.Fatalf("simple frame=%d want=66", got)
	}
	if got := len(EncodeFull(model.FullStatus{})); got != 132 {
		t.Fatalf("full frame=%d want=132", got)
	}
}

func TestSubscribeSimpleGolden(t *testing.T) {
	want, _ := hex.DecodeString("474e53530101000101")
	got := EncodeSubscribeRequest(StatusSimple)
	if !bytes.Equal(got, want) {
		t.Fatalf("got=%x want=%x", got, want)
	}
}

func TestSwitchGolden(t *testing.T) {
	want, _ := hex.DecodeString("474e535301100006010203040103")
	got := EncodeSwitchRequest(SwitchRequest{RequestID: 0x01020304, Enabled: 1, Type: TypeGPSBeiDou})
	if !bytes.Equal(got, want) {
		t.Fatalf("got=%x want=%x", got, want)
	}
}

func TestStatusUsesBigEndianAndExactOffsets(t *testing.T) {
	s := model.SimpleStatus{
		FieldValidityMask: model.SimpleRecvValid,
		RecvTime:          1,
	}
	frame := EncodeSimple(s)
	if frame[5] != TypeStatusSimple || frame[6] != 0 || frame[7] != 58 {
		t.Fatalf("bad header: %x", frame[:8])
	}
	if !bytes.Equal(frame[8:16], []byte{0, 0, 0, 0, 0, 0, 0, 2}) ||
		!bytes.Equal(frame[24:32], []byte{0, 0, 0, 0, 0, 0, 0, 1}) {
		t.Fatalf("bad payload: %x", frame[8:])
	}
}
```

- [ ] **Step 2: Write failing stream resynchronization tests**

Add:

```go
func TestDecoderHandlesGarbagePartialAndStickyFrames(t *testing.T) {
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
}

func TestDecoderResynchronizesAfterOversizedLength(t *testing.T) {
	d := NewDecoder(1024)
	bad := []byte{'G', 'N', 'S', 'S', 1, 1, 0x04, 0x01}
	frames := d.Feed(append(bad, EncodeSubscribeRequest(StatusFull)...))
	if len(frames) != 1 || frames[0].Type != TypeSubscribeRequest {
		t.Fatalf("failed to resynchronize: %+v", frames)
	}
}
```

- [ ] **Step 3: Run protocol tests and verify failure**

Run:

```powershell
go test ./internal/protocol -v
```

Expected: FAIL because encoders and the decoder do not exist.

- [ ] **Step 4: Implement header/message definitions and exact encoders**

Create `header.go`:

```go
package protocol

const (
	Magic         = "GNSS"
	Version uint8 = 1
	HeaderSize     = 8
	MaxPayload     = 1024
)

const (
	TypeSubscribeRequest uint8 = 0x01
	TypeSubscribeACK     uint8 = 0x02
	TypeStatusFull       uint8 = 0x03
	TypeStatusSimple     uint8 = 0x04
	TypeSwitchRequest    uint8 = 0x10
	TypeSwitchACK        uint8 = 0x11
)

func frame(messageType uint8, payload []byte) []byte {
	out := make([]byte, HeaderSize+len(payload))
	copy(out[:4], Magic)
	out[4] = Version
	out[5] = messageType
	binary.BigEndian.PutUint16(out[6:8], uint16(len(payload)))
	copy(out[8:], payload)
	return out
}
```

Create `messages.go` with exact enums and payload types from the protocol. Use `Enabled` as the Go field name because `switch` is a language keyword:

```go
type SwitchRequest struct {
	RequestID uint32
	Enabled   uint8
	Type      uint8
}

type SwitchACK struct {
	RequestID uint32
	Result    uint8
}
```

Create `status.go`. Encode every integer with `binary.BigEndian` and every float using `math.Float32bits` or `math.Float64bits`. Write fields sequentially at the documented offsets; do not use `unsafe` or struct memory copies. Assert final cursor values in tests: 58 for SIMPLE and 124 for FULL.

- [ ] **Step 5: Implement bounded stream decoding and request parsing**

Create `decoder.go` with:

```go
type Frame struct {
	Version uint8
	Type    uint8
	Payload []byte
}

type Decoder struct {
	maxPayload int
	buf        []byte
}

func NewDecoder(maxPayload int) *Decoder {
	return &Decoder{maxPayload: maxPayload}
}
```

`Feed` must scan for `GNSS`, retain at most the last three unmatched bytes, wait for a full 8-byte header, reject lengths over 1024 by dropping one byte from the current candidate magic, wait for partial frames, and emit copies of complete payloads. Unknown type/version is retained in `Frame` so the session layer can skip it without disconnecting.

Add exact payload parsers that reject wrong lengths:
- subscribe request: 1
- switch request: 6
- subscribe ACK: 1
- switch ACK: 5.

- [ ] **Step 6: Run protocol tests, race tests, and commit**

Run:

```powershell
gofmt -w internal/protocol
go test ./internal/protocol -v
go test -race ./internal/protocol
git add internal/protocol
git commit -m "feat: implement GNSS binary protocol v1"
```

Expected: fixed sizes, golden frames, byte order, partial/sticky input, oversized-length recovery, and unknown-type skipping all PASS.

### Task 7: Implement Linux serial access and UART telemetry

**Files:**
- Create: `internal/serial/port.go`
- Create: `internal/serial/port_linux.go`
- Create: `internal/serial/port_unsupported.go`
- Create: `internal/serial/icount_linux.go`
- Create: `internal/serial/endpoint.go`
- Test: `internal/serial/endpoint_test.go`
- Create: `internal/serial/metrics.go`
- Test: `internal/serial/metrics_test.go`

- [ ] **Step 1: Write failing baud, occupancy, and rolling-window tests**

Create `internal/serial/metrics_test.go`:

```go
package serial

import (
	"testing"
	"time"
)

func TestOccupancy8N1(t *testing.T) {
	if got := Occupancy(768, time.Second, 9600); got != 0.8 {
		t.Fatalf("occupancy=%v want=0.8", got)
	}
}

func TestTrackerExposesOneSecondPeakAndSixtySecondAverage(t *testing.T) {
	tr := NewTracker(9600)
	start := time.Unix(0, 0)
	for i := 0; i < 60; i++ {
		bytes := 720
		if i == 20 {
			bytes = 900
		}
		tr.Add(start.Add(time.Duration(i)*time.Second), bytes)
	}
	s := tr.Snapshot(start.Add(60 * time.Second))
	if s.PeakOneSecond < 0.93 || s.AverageSixtySeconds < 0.75 || s.AverageSixtySeconds > 0.76 {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```powershell
go test ./internal/serial -run "TestOccupancy|TestTracker" -v
```

Expected: FAIL because telemetry functions do not exist.

- [ ] **Step 3: Define the portable port contract and telemetry**

Create `internal/serial/port.go`:

```go
package serial

import "io"

type ICount struct {
	RX          uint32
	TX          uint32
	Frame       uint32
	Overrun     uint32
	Parity      uint32
	BufferOverrun uint32
}

type Port interface {
	io.ReadWriteCloser
	ICount() (ICount, error)
}

func Open(path string, baud int) (Port, error) {
	return open(path, baud)
}
```

Create `metrics.go` with:

```go
type Snapshot struct {
	PeakOneSecond       float64
	AverageSixtySeconds float64
}

func Occupancy(bytes int, window time.Duration, baud int) float64 {
	if bytes < 0 || window <= 0 || baud <= 0 {
		return 0
	}
	return float64(bytes*10) / (float64(baud) * window.Seconds())
}
```

Implement `Tracker` as a 60-bucket ring indexed by Unix second. `Snapshot` returns the maximum occupancy among retained one-second buckets and the total bytes over the last 60 seconds divided by `baud × 60 / 10`. Empty buckets count as zero.

- [ ] **Step 4: Implement Linux termios, exclusive access, and ICount**

Create `port_linux.go` with build tag `//go:build linux`. Open using:

```go
fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
```

Immediately issue `TIOCEXCL`. Configure raw 8N1, `CLOCAL|CREAD`, no software/hardware flow control, `VMIN=1`, `VTIME=0`. Support 9600, 19200, 38400, 57600, and 115200 via an explicit map to `unix.B9600` and related constants; reject all other baud values with a descriptive error. Ensure every configuration failure closes the fd.

Create `icount_linux.go` with the Linux kernel layout:

```go
type serialICounter struct {
	CTS, DSR, RNG, DCD int32
	RX, TX             int32
	Frame, Overrun     int32
	Parity, Break      int32
	BufferOverrun      int32
	Reserved           [9]int32
}
```

Call `unix.Syscall(unix.SYS_IOCTL, fd, unix.TIOCGICOUNT, uintptr(unsafe.Pointer(&raw)))` and copy counters to unsigned public fields. Return `unix.ENOTTY` or the actual errno when unsupported; do not invent zero success.

Create `port_unsupported.go` with build tag `//go:build !linux`; its `open` returns `errors.New("GNSS UART is supported only on Linux")` so host unit tests compile.

- [ ] **Step 5: Add buffered read-loop coverage**

Add a test around a fake reader that records requested buffer lengths. Implement:

```go
func ReadLoop(ctx context.Context, r io.Reader, onChunk func([]byte)) error {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			onChunk(append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			if errors.Is(err, io.EOF) && ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}
```

The fake reader test must assert that every `Read` receives a 4096-byte buffer, preventing regression to one-byte syscalls.

Add a concurrency-safe `Endpoint` that owns the currently open port. `Set` replaces the current port, `Clear` removes only the matching port, and `Write` returns `ErrUnavailable` when no port is present. The controller writes through this endpoint so UART reconnects do not require rebuilding the TCP/control stack. Test concurrent `Set`, `Clear`, and `Write` with `go test -race`.

- [ ] **Step 6: Cross-compile the serial package and commit**

Run:

```powershell
gofmt -w internal/serial
go test ./internal/serial -v
go mod tidy
New-Item -ItemType Directory -Force build/dist | Out-Null
$env:CGO_ENABLED="0"
$env:GOOS="linux"
$env:GOARCH="arm64"
go test -c -o build/dist/serial-arm64.test ./internal/serial
Remove-Item "build/dist/serial-arm64.test"
git add go.mod go.sum internal/serial
git commit -m "feat: add exclusive Linux GNSS serial access"
```

Expected: host tests PASS; Linux arm64 test binary builds with CGO disabled; only the explicit temporary test binary is removed.

### Task 8: Implement GNSS command control

**Files:**
- Create: `internal/control/controller.go`
- Test: `internal/control/controller_test.go`

- [ ] **Step 1: Write failing tests for off, no-op, power-on, mode change, and busy behavior**

Create fakes in `controller_test.go`:

```go
type fakeWriter struct {
	writes []string
	err    error
}

func (f *fakeWriter) Write(p []byte) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.writes = append(f.writes, string(p))
	return len(p), nil
}

type fakeObserver struct {
	lastNMEA time.Time
	talker   string
	updates  chan string
}
```

Add tests with these exact expectations:

```go
func TestSwitchOffWritesNothingAndClearsCycle(t *testing.T) {
	writer := &fakeWriter{}
	cleared := false
	c := New(writer, &fakeObserver{}, func() { cleared = true })
	ack := c.Handle(context.Background(), protocol.SwitchRequest{RequestID: 1, Enabled: 0, Type: protocol.TypeGPS})
	if ack.Result != protocol.SwitchSuccess || len(writer.writes) != 0 || !cleared {
		t.Fatalf("ack=%+v writes=%v cleared=%v", ack, writer.writes, cleared)
	}
}

func TestMatchingModeHasNoSerialSideEffects(t *testing.T) {
	writer := &fakeWriter{}
	observer := &fakeObserver{lastNMEA: time.Now(), talker: "GN"}
	c := New(writer, observer, func() {})
	ack := c.Handle(context.Background(), protocol.SwitchRequest{RequestID: 2, Enabled: 1, Type: protocol.TypeGPSBeiDou})
	if ack.Result != protocol.SwitchSuccess || len(writer.writes) != 0 {
		t.Fatalf("ack=%+v writes=%v", ack, writer.writes)
	}
}

func TestModeChangeWritesConfigAndSave(t *testing.T) {
	writer := &fakeWriter{}
	observer := &fakeObserver{lastNMEA: time.Now(), talker: "GP", updates: make(chan string, 1)}
	observer.updates <- "GN"
	c := New(writer, observer, func() {})
	ack := c.Handle(context.Background(), protocol.SwitchRequest{RequestID: 3, Enabled: 1, Type: protocol.TypeGPSBeiDou})
	if ack.Result != protocol.SwitchSuccess {
		t.Fatalf("ack=%+v", ack)
	}
	want := []string{"$CFGSYS,h11\n", "$CFGSAVE\n"}
	if !reflect.DeepEqual(writer.writes, want) {
		t.Fatalf("writes=%q want=%q", writer.writes, want)
	}
}
```

Also add tests for:
- known OFF → ON waits through an injected sleeper then writes `$RESET,0,h00\n`;
- unknown state with no NMEA in 3 seconds follows the same power-on flow;
- timeout with no post-command NMEA returns `TIMEOUT`;
- wrong post-command talker returns `VERIFY_FAILED`;
- write error returns `SERIAL_UNAVAILABLE`;
- invalid enabled/type returns `INVALID_ARGUMENT`;
- a second concurrent call receives `BUSY`.

- [ ] **Step 2: Run control tests and verify failure**

Run:

```powershell
go test ./internal/control -v
```

Expected: FAIL because `Controller` does not exist.

- [ ] **Step 3: Implement serialized, idempotent control**

Define the observer and clock injection:

```go
type Observer interface {
	LastNMEA() time.Time
	CurrentTalker() string
	WaitTalker(ctx context.Context) (string, bool)
}

type Controller struct {
	writer   io.Writer
	observer Observer
	clear    func()
	sleep    func(context.Context, time.Duration) error
	now      func() time.Time
	busy     chan struct{}
	state    powerState
}
```

Use a capacity-one `busy` channel for non-blocking serialization. Keep `powerUnknown`, `powerOff`, and `powerOn` only in memory. Validate every request before side effects. For power-on:
1. if known OFF, or unknown with no checksum-valid NMEA in the preceding 3 seconds, sleep 500 ms and write hot reset;
2. compare current talker to requested type;
3. if mismatched, write the exact `CFGSYS` line then `$CFGSAVE\n`;
4. wait up to 5 seconds from the last command for one expected RMC/GGA talker;
5. return TIMEOUT when no correct NMEA arrives and VERIFY_FAILED when correct NMEA arrives with only wrong talkers.

If any serial command was written, success must be based only on RMC/GGA observations received after the last command write; never accept a stale pre-command talker. When no command is needed, a recent matching talker may produce immediate success.

Use these mappings:

```go
var mode = map[uint8]struct {
	command string
	talkers map[string]bool
}{
	protocol.TypeGPS:       {command: "$CFGSYS,h1\n", talkers: map[string]bool{"GP": true}},
	protocol.TypeBeiDou:    {command: "$CFGSYS,h10\n", talkers: map[string]bool{"BD": true, "GB": true}},
	protocol.TypeGPSBeiDou: {command: "$CFGSYS,h11\n", talkers: map[string]bool{"GN": true}},
}
```

Do not persist requested state. TCP reconnection never invokes `Handle` automatically.

- [ ] **Step 4: Run tests and commit**

Run:

```powershell
gofmt -w internal/control
go test ./internal/control -v
go test -race ./internal/control
git add internal/control
git commit -m "feat: execute loopback GNSS controls"
```

Expected: all control results, serial command sequences, no-op behavior, timing, and BUSY behavior PASS.

### Task 9: Implement bounded TCP sessions, subscriptions, and publication

**Files:**
- Create: `internal/server/limits.go`
- Create: `internal/server/session.go`
- Create: `internal/server/server.go`
- Test: `internal/server/server_test.go`

- [ ] **Step 1: Write failing admission-limit tests**

Test the pure limiter first:

```go
func TestLimiterReservesOneLoopbackSlot(t *testing.T) {
	l := NewLimiter(5, 4)
	for i := 0; i < 4; i++ {
		if !l.Acquire(false) {
			t.Fatalf("remote %d rejected", i)
		}
	}
	if l.Acquire(false) {
		t.Fatal("fifth remote must be rejected")
	}
	if !l.Acquire(true) {
		t.Fatal("loopback control slot must remain available")
	}
	if l.Acquire(true) {
		t.Fatal("total connection limit must remain five")
	}
}

func TestReleaseReturnsCorrectBucket(t *testing.T) {
	l := NewLimiter(5, 4)
	l.Acquire(false)
	l.Release(false)
	if !l.Acquire(false) {
		t.Fatal("remote slot was not released")
	}
}
```

- [ ] **Step 2: Write failing session behavior tests with `net.Pipe`**

Cover:
- first valid message must be SUBSCRIBE;
- 5-second deadline closes an idle admitted connection using an injected shorter test duration;
- SIMPLE subscription receives only type `0x04`;
- FULL subscription receives only type `0x03`;
- repeated subscription returns `ALREADY_SUBSCRIBED`;
- malformed/unknown frames do not close the connection and the next valid frame is parsed;
- remote switch receives `FORBIDDEN`;
- loopback switch invokes the injected controller;
- latest-value queue replaces an unwritten old status;
- write timeout closes only that session.

Use a fake controller:

```go
type controlHandler interface {
	Handle(context.Context, protocol.SwitchRequest) protocol.SwitchACK
}
```

- [ ] **Step 3: Run server tests and verify failure**

Run:

```powershell
go test ./internal/server -v
```

Expected: FAIL because the limiter, server, and session do not exist.

- [ ] **Step 4: Implement admission and non-blocking over-limit rejection**

Create `limits.go`:

```go
type Limiter struct {
	mu        sync.Mutex
	total     int
	remote    int
	maxTotal  int
	maxRemote int
}

func (l *Limiter) Acquire(loopback bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.maxTotal || (!loopback && l.remote >= l.maxRemote) {
		return false
	}
	l.total++
	if !loopback {
		l.remote++
	}
	return true
}

func (l *Limiter) Release(loopback bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total--
	if !loopback {
		l.remote--
	}
}
```

When `Acquire` fails, set the accepted TCP connection's read deadline to `time.Now()` and perform at most one read into a 9-byte buffer. If a complete valid SUBSCRIBE_REQUEST was already buffered, attempt SUBSCRIBE_ACK/SERVER_FULL with a 100 ms write deadline; otherwise close immediately. Never use the 5-second subscription deadline for a rejected connection.

- [ ] **Step 5: Implement session state and latest-only writes**

Each admitted session owns:
- one `protocol.Decoder`;
- a 5-second subscription deadline;
- selected SIMPLE/FULL type;
- a capacity-one `chan []byte` output queue;
- a writer goroutine with a 3-second write deadline.

Implement replacement without blocking:

```go
func offerLatest(ch chan []byte, frame []byte) {
	copyFrame := append([]byte(nil), frame...)
	select {
	case ch <- copyFrame:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- copyFrame:
	default:
	}
}
```

After successful subscription clear the read deadline. Protocol-format errors only increment counters and resynchronize. Network EOF, read/write error, write timeout, subscription timeout, or server shutdown closes the session.

- [ ] **Step 6: Implement server broadcasting and loopback control authorization**

`Server.Publish(status)` encodes FULL once and SIMPLE once, then offers the selected frame to each subscribed session. Determine loopback from `net.TCPAddr.IP.IsLoopback()` but authorize control only when the IPv4 address is exactly `127.0.0.1`, matching the specification.

A single connection both subscribes and sends control. Do not create a second listener or control socket.

- [ ] **Step 7: Run server tests, race tests, and commit**

Run:

```powershell
gofmt -w internal/server
go test ./internal/server -v
go test -race ./internal/server
git add internal/server
git commit -m "feat: serve bounded GNSS subscriptions"
```

Expected: all limit, timeout, protocol recovery, format selection, slow-client isolation, and loopback-control tests PASS.

### Task 10: Wire serial input, aggregation, observation, and logging

**Files:**
- Create: `internal/observe/stats.go`
- Test: `internal/observe/stats_test.go`
- Create: `internal/app/app.go`
- Test: `internal/app/app_test.go`
- Create: `cmd/gnssagent/main.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Add failing tests for talker observation and rate-limited counters**

Create `internal/observe/stats_test.go`:

```go
package observe

import (
	"testing"
	"time"
)

func TestTalkerStateTracksOnlyRMCAndGGA(t *testing.T) {
	s := NewState()
	now := time.Unix(10, 0)
	s.Observe("GN", "GSV", now)
	if !s.LastNMEA().IsZero() {
		t.Fatal("GSV must not establish control verification talker")
	}
	s.Observe("GN", "RMC", now)
	if s.CurrentTalker() != "GN" || !s.LastNMEA().Equal(now) {
		t.Fatalf("unexpected state: talker=%s last=%v", s.CurrentTalker(), s.LastNMEA())
	}
}

func TestCountersDrainAsDeltas(t *testing.T) {
	c := NewCounters()
	c.AddChecksumError()
	c.AddChecksumError()
	first := c.Drain()
	second := c.Drain()
	if first.ChecksumErrors != 2 || second.ChecksumErrors != 0 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}
```

- [ ] **Step 2: Write failing application pipeline tests**

Use injected dependencies:

```go
type fakePublisher struct {
	statuses []model.FullStatus
}

func TestNoNMEAProducesNoStatus(t *testing.T) {
	publisher := &fakePublisher{}
	app := newTestApp(strings.NewReader(""), publisher)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = app.Run(ctx)
	if len(publisher.statuses) != 0 {
		t.Fatal("no input must remain silent")
	}
}

func TestValidNMEACyclePublishesOneStatus(t *testing.T) {
	input := strings.NewReader(validFixtureForTwoUTCSeconds(t))
	publisher := &fakePublisher{}
	app := newTestApp(input, publisher)
	if err := app.runSerialSession(context.Background(), nopPort{Reader: input}); err != io.EOF {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(publisher.statuses) != 1 {
		t.Fatalf("statuses=%d want=1", len(publisher.statuses))
	}
}

func TestSerialOpenFailureDoesNotStopTCPServer(t *testing.T) {
	started := make(chan struct{})
	app := newTestAppWithOpenError(errors.New("busy"), started)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.Run(ctx)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("server did not start while serial was unavailable")
	}
}
```

- [ ] **Step 3: Run observe/app tests and verify failure**

Run:

```powershell
go test ./internal/observe ./internal/app -v
```

Expected: FAIL because the packages do not exist.

- [ ] **Step 4: Implement observation state and counters**

`observe.State` stores last checksum-valid RMC/GGA talker and time under a mutex and notifies waiters through a replaceable capacity-one channel. Its `WaitTalker` returns the next observed talker or context timeout. `Counters` uses atomics for checksum failures, parse failures, published cycles, protocol errors, slow-client replacements, UART bytes, and UART errors; `Drain` swaps deltas to zero for limited periodic logging.

- [ ] **Step 5: Implement the application coordinator**

Define narrow dependencies:

```go
type Publisher interface {
	Publish(model.FullStatus)
	Run(context.Context) error
}

type SerialOpener func(string, int) (serial.Port, error)

type App struct {
	cfg        config.Config
	open       SerialOpener
	publisher  Publisher
	aggregate  *aggregate.Aggregator
	observer   *observe.State
	counters   *observe.Counters
	logger     *slog.Logger
	retryDelay time.Duration
	now        func() time.Time
}
```

`Run` starts the TCP server first, then a serial supervisor. The supervisor:
1. attempts `Open`;
2. on failure logs and retries after 2 seconds without stopping TCP;
3. on success starts a 4096-byte `serial.ReadLoop`;
4. feeds each chunk to one `nmea.Framer`;
5. validates/parses each complete line;
6. optionally logs the raw line only when `--log-raw-nmea` is enabled;
7. updates talker observation only for checksum-correct RMC/GGA;
8. feeds the aggregator and publishes completed cycles;
9. checks `FlushExpired` from a 100 ms ticker;
10. feeds received byte counts into `serial.Tracker`, warns when rolling 1-second peak exceeds 90% or 60-second average exceeds 80%;
11. snapshots `ICount` every 60 seconds, logs counter deltas, and explicitly logs unsupported ioctl so device acceptance can use the `/proc` fallback;
12. closes, clears the shared serial endpoint, and reopens the port after read errors.

On shutdown, cancel goroutines, close the port to unblock reads, close TCP sessions, and send no GNSS command.

- [ ] **Step 6: Add opt-in raw capture configuration**

Extend `config.Config`:

```go
LogRawNMEA bool
```

Register:

```go
fs.BoolVar(&cfg.LogRawNMEA, "log-raw-nmea", false, "log every checksum-valid NMEA sentence at debug level")
```

Add a test proving the default is false and the flag enables it. Raw NMEA must never be logged at info level.

- [ ] **Step 7: Implement the executable entry point**

Create `cmd/gnssagent/main.go`:

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gnssagent/internal/app"
	"gnssagent/internal/buildinfo"
	"gnssagent/internal/config"
)

func main() {
	cfg, err := config.Parse(os.Args[1:], buildinfo.Target)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	now := time.Now()
	logger.Info("starting GNSSAgent",
		"target", buildinfo.Target,
		"system_unix_ms", now.UnixMilli(),
		"system_utc", now.UTC().Format(time.RFC3339Nano),
		"serial", cfg.SerialDevice,
		"baud", cfg.Baud,
		"listen", cfg.ListenAddress,
	)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.New(cfg, logger).Run(ctx); err != nil {
		logger.Error("GNSSAgent stopped", "error", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 8: Run end-to-end host tests and commit**

Run:

```powershell
gofmt -w cmd/gnssagent internal/app internal/observe internal/config
go test ./internal/observe ./internal/app ./internal/config -v
go test -race ./internal/observe ./internal/app
go vet ./cmd/gnssagent ./internal/app ./internal/observe
git add cmd/gnssagent internal/app internal/observe internal/config
git commit -m "feat: run GNSSAgent service pipeline"
```

Expected: silent-no-data, one-status-per-cycle, serial-retry-with-live-TCP, observation, counter, shutdown, and raw-log gating tests PASS.

### Task 11: Add reproducible target builds

**Files:**
- Create: `build/scripts/build.ps1`
- Create: `build/scripts/build-hf.ps1`
- Create: `build/scripts/build.bat`
- Create: `tests/build.test.ps1`

- [ ] **Step 1: Write the failing PowerShell build contract test**

Create `tests/build.test.ps1`:

```powershell
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$main = Get-Content -Raw (Join-Path $root "build\scripts\build.ps1")
$hf = Get-Content -Raw (Join-Path $root "build\scripts\build-hf.ps1")

$requiredMain = @(
    'go1.25.5',
    'GNSSAgent-CCU',
    'GNSSAgent-MultibandRadio',
    'GNSSAgent-MultibandHandheld',
    '-GOARM64 "v8.0"',
    '-GOARM "7"',
    'CGO_ENABLED = "0"',
    '-trimpath',
    '-s -w',
    'gnssagent/internal/buildinfo.Target'
)
foreach ($text in $requiredMain) {
    if (-not $main.Contains($text)) { throw "build.ps1 missing: $text" }
}
if ($main.Contains("CCU-Audio")) { throw "audio target must not be built" }
if (-not $hf.Contains("go1.23.12") -or -not $hf.Contains("GNSSAgent-HF")) {
    throw "HF toolchain/target is incorrect"
}
"build contract OK"
```

- [ ] **Step 2: Run the contract test and verify failure**

Run:

```powershell
pwsh -File tests/build.test.ps1
```

Expected: FAIL because build scripts do not exist.

- [ ] **Step 3: Implement the Go 1.25.5 build script**

Follow the compiler archive/cache pattern used by `D:\CPD\CPDC`. `build.ps1` must validate `go version go1.25.5 windows/amd64`, restore every environment variable in `finally`, and call one function for:

```powershell
Build-GNSSAgent -Name "GNSSAgent-CCU" -GOARCH "amd64" -Target "ccu"
Build-GNSSAgent -Name "GNSSAgent-MultibandRadio" -GOARCH "arm64" -GOARM64 "v8.0" -Target "multiband-radio"
Build-GNSSAgent -Name "GNSSAgent-MultibandHandheld" -GOARCH "arm" -GOARM "7" -Target "multiband-handheld"
```

The build invocation is:

```powershell
& $goExe build -trimpath -ldflags "-s -w -X gnssagent/internal/buildinfo.Target=$Target" -o $destination ./cmd/gnssagent
if ($LASTEXITCODE -ne 0) { throw "build failed for $Name" }
```

Use `build/compiler/go1.25.5.windows-amd64.zip` and cache extraction under `build/compiler/.go1.25.5`. Output to `build/dist/bin`.

- [ ] **Step 4: Implement the isolated HF build**

`build-hf.ps1` uses `build/compiler/go1.23.12.windows-amd64.zip`, validates the exact version, and builds:

```powershell
$env:GOOS = "linux"
$env:GOARCH = "arm"
$env:GOARM = "7"
$env:CGO_ENABLED = "0"
& $goExe build -trimpath -ldflags "-s -w -X gnssagent/internal/buildinfo.Target=hf" `
    -o (Join-Path $outputRoot "GNSSAgent-HF") ./cmd/gnssagent
```

Do not build `GNSSAgent-CCU-Audio`.

- [ ] **Step 5: Add the batch entry point and run build checks**

Create `build/scripts/build.bat`:

```bat
@echo off
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1"
if errorlevel 1 exit /b %errorlevel%
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-hf.ps1"
exit /b %errorlevel%
```

Run:

```powershell
pwsh -File tests/build.test.ps1
pwsh -File build/scripts/build.ps1
pwsh -File build/scripts/build-hf.ps1
Get-ChildItem build/dist/bin | Select-Object Name,Length
```

Expected: exactly `GNSSAgent-CCU`, `GNSSAgent-HF`, `GNSSAgent-MultibandRadio`, and `GNSSAgent-MultibandHandheld` exist; no audio binary exists.

- [ ] **Step 6: Commit build tooling**

Run:

```powershell
git add build/scripts tests/build.test.ps1
git commit -m "build: add GNSSAgent target toolchains"
```

Expected: commit succeeds without adding compiler caches or output binaries.

### Task 12: Add SysV deployment and device acceptance scripts

**Files:**
- Create: `deploy/default/gnssagent`
- Create: `deploy/init.d/gnssagent`
- Create: `tests/device/smoke.sh`
- Create: `tests/device/capture-link-budget.sh`

- [ ] **Step 1: Create the MultibandRadio default configuration**

Create `deploy/default/gnssagent`:

```sh
GNSSAGENT_BIN=/usr/bin/GNSSAgent-MultibandRadio
GNSSAGENT_ARGS="--serial /dev/ttyUL4 --baud 9600 --listen 0.0.0.0:29501 --max-connections 5 --max-remote-connections 4"
GNSSAGENT_LOG=/var/log/gnssagent.log
```

- [ ] **Step 2: Create a non-destructive SysV init script**

`deploy/init.d/gnssagent` must implement `start`, `stop`, `restart`, and `status`. Use a PID file at `/var/run/gnssagent.pid`, send TERM on stop, wait up to 5 seconds, and never issue RESET/CFGSYS/CFGSAVE or manipulate GPIO. Before start, verify the binary is executable and the serial path is configured. Redirect stdout/stderr to the configured log.

The start command must use:

```sh
start-stop-daemon --start --background --make-pidfile \
    --pidfile "$PIDFILE" --exec "$GNSSAGENT_BIN" -- \
    $GNSSAGENT_ARGS >>"$GNSSAGENT_LOG" 2>&1
```

If this BusyBox image lacks a required `start-stop-daemon` option, replace only the unsupported option with a shell background launch and explicit `echo $! > "$PIDFILE"`; preserve the same TERM shutdown behavior.

- [ ] **Step 3: Create the device smoke test**

`tests/device/smoke.sh` performs read-only checks and exits nonzero on failure:

```sh
#!/bin/sh
set -eu

if [ "$#" -gt 0 ]; then
    BIN=$1
else
    BIN=/usr/bin/GNSSAgent-MultibandRadio
fi
test -x "$BIN"
"$BIN" --help >/dev/null 2>&1 || true
uname -m
stty -F /dev/ttyUL4 -a
(ss -ltn 2>/dev/null || netstat -ltn) | grep ':29501'
pidof copy_RadioApp || true
pidof GNSSAgent-MultibandRadio
grep -E 'system_unix_ms|serial|baud|listen' /var/log/gnssagent.log | tail -n 5
```

Do not kill `copy_RadioApp`. The operator must first deploy the modified version that no longer opens `/dev/ttyUL4`.

- [ ] **Step 4: Create one-shot link-budget capture**

`capture-link-budget.sh` accepts output directory and duration, creates new timestamped files, and never deletes existing data. It requires the normal service to be stopped and exits with instructions if the GNSSAgent process is already running. Launch the binary temporarily with `--log-level debug --log-raw-nmea` and a timeout.

Capture:
- start/end `/proc/tty/driver/ttyUL4`;
- process log with every checksum-valid NMEA line;
- per-second RX byte deltas;
- GPGSV/BDGSV packet counts;
- every GNGSA raw line;
- RMC date/status and GGA quality/geoid fields;
- service's TIOCGICOUNT support/result.

The script must calculate:

```sh
occupancy_tenths_percent=$((bytes_this_second * 10000 / 9600))
```

This integer is tenths of a percent because 8N1 uses 10 bits per byte. Flag any rolling one-second value over 900 (90.0%) and any 60-second average over 800 (80.0%). Preserve the raw files for host-side review.

- [ ] **Step 5: Validate shell syntax and commit**

Run on a Linux shell or WSL:

```sh
sh -n deploy/init.d/gnssagent
sh -n tests/device/smoke.sh
sh -n tests/device/capture-link-budget.sh
```

Expected: all syntax checks exit 0.

Run:

```powershell
git add deploy tests/device
git commit -m "ops: add GNSSAgent SysV deployment and device tests"
```

Expected: deployment and acceptance scripts are committed; no device state has been changed during host validation.

### Task 13: Final conformance and regression verification

**Files:**
- Create: `tests/protocol_doc.test.ps1`

- [ ] **Step 1: Add a protocol-document conformance test**

Create `tests/protocol_doc.test.ps1` to parse both status layout tables in `docs/protocol/GNSSAgent-Binary-Protocol-v1.md`. It must assert:
- SIMPLE offsets are contiguous and total 58, bits are 0–8;
- FULL offsets are contiguous and total 124, bits are 0–28;
- every message row satisfies `frame = payload + 8`;
- the SIMPLE layout golden vector contains 66 bytes;
- document version is 1.2 and wire protocol version is 1.

Use the same table-row regex exercised during design review:

```powershell
'^\| (\d+) \| (\d+) \| (uint\d+|float\d+) \| (—|\d+) \| `([^`]+)` \|'
```

- [ ] **Step 2: Run the complete host verification suite**

Run:

```powershell
gofmt -w cmd internal
go test ./... -count=1
go test -race ./internal/aggregate ./internal/protocol ./internal/control ./internal/server ./internal/app
go vet ./...
pwsh -File tests/protocol_doc.test.ps1
pwsh -File tests/build.test.ps1
```

Expected: all commands exit 0 with no test, race, vet, protocol-layout, or build-contract failures.

- [ ] **Step 3: Produce coverage evidence for critical packages**

Run:

```powershell
go test ./internal/nmea ./internal/aggregate ./internal/protocol ./internal/control ./internal/server `
    -coverprofile coverage.txt
go tool cover -func coverage.txt
```

Expected: every critical package reports executed statements, and the named protocol-result, NMEA-validity, aggregate-ambiguity, and control-ACK cases from Tasks 4–9 appear in the coverage run.

- [ ] **Step 4: Rebuild all four production binaries**

Run:

```powershell
pwsh -File build/scripts/build.ps1
pwsh -File build/scripts/build-hf.ps1
Get-ChildItem build/dist/bin | Sort-Object Name | Select-Object Name,Length
```

Expected: four non-empty target binaries and no `GNSSAgent-CCU-Audio`.

- [ ] **Step 5: Run MultibandRadio integration and acceptance**

After the modified `copy_RadioApp` releases `/dev/ttyUL4`:

1. deploy the MultibandRadio binary and SysV files;
2. run `tests/device/smoke.sh`;
3. connect an antenna and run `capture-link-budget.sh` for at least 10 minutes;
4. verify one-second peak ≤90%, 60-second average ≤80%, and zero UART overrun/frame/parity deltas;
5. inspect GSA PRN/System ID behavior, DOP raw strings, GSV completeness, RMC date, and geoid separation;
6. run four continuous LAN subscribers and confirm a fifth loopback `copy_RadioApp` session can subscribe and control;
7. disconnect/reconnect TCP and prove no RESET/CFGSYS/CFGSAVE writes occur;
8. run the service for 24 hours and check process survival, memory stability, no stale-field carry, and no growing error counts.

Expected: every requirement in sections 15.4, 15.5, and 17 of the approved specification has recorded evidence.

- [ ] **Step 6: Commit the conformance test**

Run:

```powershell
git add tests/protocol_doc.test.ps1
git status --short
git commit -m "test: verify protocol document conformance"
```

Expected: the commit contains the protocol-document conformance test; generated binaries, compiler caches, and `coverage.txt` remain ignored.

## Completion checklist

- [ ] All Go tests, race tests, vet checks, PowerShell contract tests, and shell syntax tests pass.
- [ ] SIMPLE/FULL sizes, offsets, masks, message lengths, and golden frames match wire protocol v1.
- [ ] No-data silence and invalid-fix publication behavior are demonstrated.
- [ ] Ordinary TCP reconnect has no GNSS hardware side effects.
- [ ] Four remote subscribers cannot consume the loopback control reservation.
- [ ] DOP lexical rounding passes the `0.4895`/`0.5005` regression.
- [ ] Buffered UART reads and TIOCGICOUNT/fallback telemetry are demonstrated.
- [ ] All four target binaries build with the required toolchain; no audio target exists.
- [ ] Antenna-on link budget and 24-hour MultibandRadio acceptance evidence are archived.
