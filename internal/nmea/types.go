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
