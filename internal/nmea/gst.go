package nmea

func parseGST(fields [][]byte) *GST {
	millis, timeValid := timeOfDay(textAt(fields, 0))
	return &GST{
		MillisOfDay:    millis,
		TimeValid:      timeValid,
		PseudorangeRMS: float64Field(textAt(fields, 1)),
		SemiMajorError: float64Field(textAt(fields, 2)),
		SemiMinorError: float64Field(textAt(fields, 3)),
		OrientationDeg: float64Field(textAt(fields, 4)),
		LatitudeError:  float64Field(textAt(fields, 5)),
		LongitudeError: float64Field(textAt(fields, 6)),
		AltitudeError:  float64Field(textAt(fields, 7)),
	}
}
