package nmea

func parseGST(fields [][]byte) *GST {
	millis, timeValid := timeOfDay(textAt(fields, 0))
	return &GST{
		MillisOfDay:    millis,
		TimeValid:      timeValid,
		PseudorangeRMS: nonNegativeFloat64Field(textAt(fields, 1)),
		SemiMajorError: nonNegativeFloat64Field(textAt(fields, 2)),
		SemiMinorError: nonNegativeFloat64Field(textAt(fields, 3)),
		OrientationDeg: float64RangeField(textAt(fields, 4), false, 0, 360, false),
		LatitudeError:  nonNegativeFloat64Field(textAt(fields, 5)),
		LongitudeError: nonNegativeFloat64Field(textAt(fields, 6)),
		AltitudeError:  nonNegativeFloat64Field(textAt(fields, 7)),
	}
}
