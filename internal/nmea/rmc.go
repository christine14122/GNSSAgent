package nmea

import "time"

func parseRMC(fields [][]byte) *RMC {
	millis, timeValid := timeOfDay(textAt(fields, 0))
	return &RMC{
		MillisOfDay: millis,
		TimeValid:   timeValid,
		Date:        dateField(textAt(fields, 8)),
		Status:      byteField(textAt(fields, 1)),
		Latitude:    coordinateField(textAt(fields, 2), textAt(fields, 3), 2, 90),
		Longitude:   coordinateField(textAt(fields, 4), textAt(fields, 5), 3, 180),
		SpeedKnots:  nonNegativeFloat64Field(textAt(fields, 6)),
		CourseDeg:   float64RangeField(textAt(fields, 7), false, 0, 360, false),
	}
}

func dateField(text string) Field[time.Time] {
	if len(text) != 6 || !decimalDigits(text) {
		return Field[time.Time]{}
	}
	// time.Parse applies the conventional NMEA/Go pivot: 00-68 are 2000-2068,
	// while 69-99 are 1969-1999. Dates have no zone, so the result is UTC.
	value, err := time.Parse("020106", text)
	if err != nil {
		return Field[time.Time]{}
	}
	return Field[time.Time]{Value: value, Valid: true}
}

func byteField(text string) Field[byte] {
	if len(text) != 1 {
		return Field[byte]{}
	}
	return Field[byte]{Value: text[0], Valid: true}
}
