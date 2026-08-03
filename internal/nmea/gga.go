package nmea

func parseGGA(fields [][]byte) *GGA {
	millis, timeValid := timeOfDay(textAt(fields, 0))
	return &GGA{
		MillisOfDay:     millis,
		TimeValid:       timeValid,
		Latitude:        coordinateField(textAt(fields, 1), textAt(fields, 2), 2, 90),
		Longitude:       coordinateField(textAt(fields, 3), textAt(fields, 4), 3, 180),
		Quality:         uint8Field(textAt(fields, 5)),
		UsedSatellites:  uint8Field(textAt(fields, 6)),
		HDOPText:        textAt(fields, 7),
		AltitudeMSL:     float64Field(textAt(fields, 8), true),
		GeoidSeparation: float64Field(textAt(fields, 10), true),
		DifferentialAge: nonNegativeFloat64Field(textAt(fields, 12)),
	}
}
