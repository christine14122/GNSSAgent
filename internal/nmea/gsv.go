package nmea

func parseGSV(fields [][]byte) *GSV {
	totalMessages, totalValid := positiveInt(textAt(fields, 0))
	messageNumber, numberValid := positiveInt(textAt(fields, 1))
	visibleCount := uint8Field(textAt(fields, 2))
	if !totalValid || !numberValid || !visibleCount.Valid || messageNumber > totalMessages {
		return &GSV{}
	}

	expectedTotal := (int(visibleCount.Value) + 3) / 4
	if expectedTotal == 0 {
		expectedTotal = 1
	}
	if totalMessages != expectedTotal {
		return &GSV{}
	}

	expectedGroups := 0
	if visibleCount.Value > 0 {
		remaining := int(visibleCount.Value) - (messageNumber-1)*4
		if remaining <= 0 {
			return &GSV{}
		}
		expectedGroups = min(4, remaining)
	}
	tailLength := len(fields) - 3
	expectedTailLength := expectedGroups * 4
	if tailLength != expectedTailLength && tailLength != expectedTailLength+1 {
		return &GSV{}
	}

	result := &GSV{
		TotalMessages: totalMessages,
		MessageNumber: messageNumber,
		VisibleCount:  visibleCount,
	}

	groupEnd := 3 + expectedTailLength
	if tailLength == expectedTailLength+1 {
		result.SignalID = uint8Field(textAt(fields, groupEnd))
	}
	for i := 3; i+3 < groupEnd; i += 4 {
		result.Satellites = append(result.Satellites, Satellite{
			PRN:       textAt(fields, i),
			Elevation: float32RangeField(textAt(fields, i+1), 0, 90, true),
			Azimuth:   float32RangeField(textAt(fields, i+2), 0, 360, false),
			// NMEA defines no universal CN0 maximum; only float32 representability limits it here.
			CN0: nonNegativeFloat32Field(textAt(fields, i+3)),
		})
	}
	return result
}
