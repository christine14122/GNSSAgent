package nmea

func parseGLL(fields [][]byte) *GLL {
	millis, timeValid := timeOfDay(textAt(fields, 4))
	return &GLL{
		MillisOfDay: millis,
		TimeValid:   timeValid,
		Latitude:    coordinateField(textAt(fields, 0), textAt(fields, 1), 2, 90),
		Longitude:   coordinateField(textAt(fields, 2), textAt(fields, 3), 3, 180),
		Status:      byteField(textAt(fields, 5)),
		Mode:        byteField(textAt(fields, 6)),
	}
}
