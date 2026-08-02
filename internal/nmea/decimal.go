package nmea

import "fmt"

func ParseMilliDecimal(text string) (int64, error) {
	if text == "" {
		return 0, fmt.Errorf("invalid decimal: empty input")
	}

	dot := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '.' {
			if dot >= 0 {
				return 0, fmt.Errorf("invalid decimal %q: multiple decimal points", text)
			}
			dot = i
			continue
		}
		if _, ok := decimalDigit(text[i]); !ok {
			return 0, fmt.Errorf("invalid decimal %q: %q is not a digit", text, text[i])
		}
	}

	integerEnd := len(text)
	if dot >= 0 {
		integerEnd = dot
	}
	if integerEnd == 0 {
		return 0, fmt.Errorf("invalid decimal %q: integer digits are required", text)
	}

	const maxInt64 = int64(1<<63 - 1)
	const maxWhole = maxInt64 / 1000
	var whole int64
	for i := 0; i < integerEnd; i++ {
		digit, _ := decimalDigit(text[i])
		if whole > (maxWhole-digit)/10 {
			return 0, fmt.Errorf("decimal %q overflows int64 milliseconds", text)
		}
		whole = whole*10 + digit
	}

	fractionStart := len(text)
	if dot >= 0 {
		fractionStart = dot + 1
	}
	var milli int64
	for place := 0; place < 3; place++ {
		milli *= 10
		index := fractionStart + place
		if index < len(text) {
			digit, _ := decimalDigit(text[index])
			milli += digit
		}
	}
	if fourth := fractionStart + 3; fourth < len(text) {
		digit, _ := decimalDigit(text[fourth])
		if digit >= 5 {
			milli++
		}
	}

	if whole > (maxInt64-milli)/1000 {
		return 0, fmt.Errorf("decimal %q overflows int64 milliseconds", text)
	}
	return whole*1000 + milli, nil
}

func decimalDigit(b byte) (int64, bool) {
	if b < '0' || b > '9' {
		return 0, false
	}
	return int64(b - '0'), true
}
