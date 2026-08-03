package nmea

func parseGSA(fields [][]byte) *GSA {
	result := &GSA{
		FixDimension: uint8Field(textAt(fields, 1)),
		PDOPText:     textAt(fields, 14),
		HDOPText:     textAt(fields, 15),
		VDOPText:     textAt(fields, 16),
		SystemID:     uint8Field(textAt(fields, 17)),
	}
	for i := 2; i <= 13; i++ {
		if prn := textAt(fields, i); prn != "" {
			result.PRNs = append(result.PRNs, prn)
		}
	}
	return result
}
