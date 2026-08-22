package nmea

import (
	"strconv"
	"time"
)

func parseZDA(fields [][]byte) *ZDA {
	millis, timeValid := timeOfDay(textAt(fields, 0))
	return &ZDA{
		MillisOfDay: millis,
		TimeValid:   timeValid,
		Date:        zdaDateField(textAt(fields, 1), textAt(fields, 2), textAt(fields, 3)),
	}
}

func zdaDateField(dayText, monthText, yearText string) Field[time.Time] {
	if len(dayText) != 2 || len(monthText) != 2 || len(yearText) != 4 ||
		!decimalDigits(dayText) || !decimalDigits(monthText) || !decimalDigits(yearText) {
		return Field[time.Time]{}
	}
	day, err := strconv.Atoi(dayText)
	if err != nil {
		return Field[time.Time]{}
	}
	month, err := strconv.Atoi(monthText)
	if err != nil {
		return Field[time.Time]{}
	}
	year, err := strconv.Atoi(yearText)
	if err != nil || year == 0 {
		return Field[time.Time]{}
	}
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if date.Year() != year || int(date.Month()) != month || date.Day() != day {
		return Field[time.Time]{}
	}
	return Field[time.Time]{Value: date, Valid: true}
}
