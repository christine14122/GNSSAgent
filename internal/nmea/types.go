package nmea

import "time"

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

type Kind uint8

const (
	KindRMC Kind = iota + 1
	KindGGA
	KindGSA
	KindGSV
	KindGST
	KindZDA
	KindGLL
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
	ZDA        *ZDA
	GLL        *GLL
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
	MillisOfDay     int64
	TimeValid       bool
	Latitude        Field[float64]
	Longitude       Field[float64]
	Quality         Field[uint8]
	UsedSatellites  Field[uint8]
	HDOPText        string
	AltitudeMSL     Field[float64]
	GeoidSeparation Field[float64]
	DifferentialAge Field[float64]
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
	MillisOfDay    int64
	TimeValid      bool
	PseudorangeRMS Field[float64]
	SemiMajorError Field[float64]
	SemiMinorError Field[float64]
	OrientationDeg Field[float64]
	LatitudeError  Field[float64]
	LongitudeError Field[float64]
	AltitudeError  Field[float64]
}

// ZDA supplies UTC time and calendar date. Its local-zone fields are not used
// because the sentence time is already UTC.
type ZDA struct {
	MillisOfDay int64
	TimeValid   bool
	Date        Field[time.Time]
}

// GLL supplies position, UTC time-of-day, navigation status, and mode. It
// carries no calendar date, so it cannot independently produce an epoch UTC.
type GLL struct {
	MillisOfDay int64
	TimeValid   bool
	Latitude    Field[float64]
	Longitude   Field[float64]
	Status      Field[byte]
	Mode        Field[byte]
}
