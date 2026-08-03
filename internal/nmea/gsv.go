package nmea

func parseGSV(fields [][]byte) *GSV {
	result := &GSV{
		TotalMessages: intValue(textAt(fields, 0)),
		MessageNumber: intValue(textAt(fields, 1)),
		VisibleCount:  uint8Field(textAt(fields, 2)),
	}

	groupEnd := len(fields)
	if remaining := len(fields) - 3; remaining > 0 && remaining%4 == 1 {
		groupEnd--
		result.SignalID = uint8Field(textAt(fields, groupEnd))
	}
	for i := 3; i+3 < groupEnd; i += 4 {
		result.Satellites = append(result.Satellites, Satellite{
			PRN:       textAt(fields, i),
			Elevation: float32Field(textAt(fields, i+1)),
			Azimuth:   float32Field(textAt(fields, i+2)),
			CN0:       float32Field(textAt(fields, i+3)),
		})
	}
	return result
}
