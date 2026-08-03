package nmea

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"time"
)

func Parse(line []byte, receivedAt time.Time) (Sentence, error) {
	if err := ValidateChecksum(line); err != nil {
		return Sentence{}, err
	}

	parts := bytes.Split(line[1:len(line)-3], []byte{','})
	identifier := parts[0]
	if len(identifier) != 5 {
		return Sentence{}, fmt.Errorf("invalid NMEA identifier %q: expected exactly five characters", identifier)
	}

	fields := parts[1:]
	sentence := Sentence{
		Talker:     string(identifier[:2]),
		ReceivedAt: receivedAt,
	}
	switch string(identifier[2:]) {
	case "RMC":
		sentence.Kind = KindRMC
		sentence.RMC = parseRMC(fields)
	case "GGA":
		sentence.Kind = KindGGA
		sentence.GGA = parseGGA(fields)
	case "GSA":
		sentence.Kind = KindGSA
		sentence.GSA = parseGSA(fields)
	case "GSV":
		sentence.Kind = KindGSV
		sentence.GSV = parseGSV(fields)
	case "GST":
		sentence.Kind = KindGST
		sentence.GST = parseGST(fields)
	default:
		return Sentence{}, fmt.Errorf("unsupported NMEA sentence type %q", identifier[2:])
	}
	return sentence, nil
}

func textAt(fields [][]byte, index int) string {
	if index < 0 || index >= len(fields) {
		return ""
	}
	return string(fields[index])
}

func float64Field(text string) Field[float64] {
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return Field[float64]{}
	}
	return Field[float64]{Value: value, Valid: true}
}

func float32Field(text string) Field[float32] {
	value, err := strconv.ParseFloat(text, 32)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return Field[float32]{}
	}
	return Field[float32]{Value: float32(value), Valid: true}
}

func uint8Field(text string) Field[uint8] {
	if !decimalDigits(text) {
		return Field[uint8]{}
	}
	value, err := strconv.ParseUint(text, 10, 8)
	if err != nil {
		return Field[uint8]{}
	}
	return Field[uint8]{Value: uint8(value), Valid: true}
}

func intValue(text string) int {
	if !decimalDigits(text) {
		return 0
	}
	value, err := strconv.ParseUint(text, 10, strconv.IntSize)
	if err != nil {
		return 0
	}
	return int(value)
}

func decimalDigits(text string) bool {
	if text == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

func coordinateField(text, hemisphere string, degreeDigits int, maxDegrees int) Field[float64] {
	if len(text) < degreeDigits+2 || len(hemisphere) != 1 {
		return Field[float64]{}
	}
	minutesText := text[degreeDigits:]
	if !decimalDigits(text[:degreeDigits]) || !decimalDigits(minutesText[:2]) {
		return Field[float64]{}
	}
	if len(minutesText) > 2 && (minutesText[2] != '.' || len(minutesText) == 3 || !decimalDigits(minutesText[3:])) {
		return Field[float64]{}
	}

	degrees, err := strconv.Atoi(text[:degreeDigits])
	if err != nil {
		return Field[float64]{}
	}
	minutes, err := strconv.ParseFloat(minutesText, 64)
	if err != nil || math.IsNaN(minutes) || math.IsInf(minutes, 0) || minutes < 0 || minutes >= 60 {
		return Field[float64]{}
	}
	if degrees > maxDegrees || (degrees == maxDegrees && minutes != 0) {
		return Field[float64]{}
	}

	value := float64(degrees) + minutes/60
	switch {
	case degreeDigits == 2 && hemisphere == "N":
	case degreeDigits == 2 && hemisphere == "S":
		value = -value
	case degreeDigits == 3 && hemisphere == "E":
	case degreeDigits == 3 && hemisphere == "W":
		value = -value
	default:
		return Field[float64]{}
	}
	return Field[float64]{Value: value, Valid: true}
}

func timeOfDay(text string) (int64, bool) {
	if len(text) < 6 || !decimalDigits(text[:6]) {
		return 0, false
	}
	if len(text) > 6 {
		if text[6] != '.' || len(text) == 7 || !decimalDigits(text[7:]) {
			return 0, false
		}
	}

	hour := int(text[0]-'0')*10 + int(text[1]-'0')
	minute := int(text[2]-'0')*10 + int(text[3]-'0')
	second := int(text[4]-'0')*10 + int(text[5]-'0')
	if hour > 23 || minute > 59 || second > 59 {
		return 0, false
	}

	millisecond := 0
	for i := 0; i < 3; i++ {
		millisecond *= 10
		fractionIndex := 7 + i
		if fractionIndex < len(text) {
			millisecond += int(text[fractionIndex] - '0')
		}
	}
	return int64(((hour*60+minute)*60+second)*1000 + millisecond), true
}
