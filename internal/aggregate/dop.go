package aggregate

import (
	"math"
	"strconv"

	"gnssagent/internal/nmea"
)

const invalidDOPMilli int64 = 127_000

type dopValue struct {
	milli int64
	value float32
}

type dopGroup struct {
	pdop dopValue
	hdop dopValue
	vdop dopValue
}

func parseDOP(text string) (dopValue, bool) {
	milli, err := nmea.ParseMilliDecimal(text)
	if err != nil || milli == invalidDOPMilli {
		return dopValue{}, false
	}
	parsed, err := strconv.ParseFloat(text, 32)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 || parsed > math.MaxFloat32 {
		return dopValue{}, false
	}
	return dopValue{milli: milli, value: float32(parsed)}, true
}

func usableGSAGroup(gsa *nmea.GSA) (dopGroup, bool) {
	if gsa == nil || !gsa.FixDimension.Valid || gsa.FixDimension.Value <= 1 {
		return dopGroup{}, false
	}
	pdop, pdopOK := parseDOP(gsa.PDOPText)
	hdop, hdopOK := parseDOP(gsa.HDOPText)
	vdop, vdopOK := parseDOP(gsa.VDOPText)
	if !pdopOK || !hdopOK || !vdopOK {
		return dopGroup{}, false
	}
	return dopGroup{pdop: pdop, hdop: hdop, vdop: vdop}, true
}

func reconcileGSADOP(groups []dopGroup) (dopGroup, bool) {
	if len(groups) == 0 {
		return dopGroup{}, false
	}
	minimum := [3]int64{groups[0].pdop.milli, groups[0].hdop.milli, groups[0].vdop.milli}
	maximum := minimum
	for _, group := range groups[1:] {
		values := [3]int64{group.pdop.milli, group.hdop.milli, group.vdop.milli}
		for index, value := range values {
			if value < minimum[index] {
				minimum[index] = value
			}
			if value > maximum[index] {
				maximum[index] = value
			}
		}
	}
	for index := range minimum {
		if maximum[index]-minimum[index] > 10 {
			return dopGroup{}, false
		}
	}
	return groups[0], true
}
