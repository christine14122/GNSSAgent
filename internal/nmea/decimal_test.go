package nmea

import "testing"

func TestParseMilliDecimalHalfUpWithoutFloat(t *testing.T) {
	tests := map[string]int64{
		"1":       1000,
		"1.2":     1200,
		"1.234":   1234,
		"1.2344":  1234,
		"1.2345":  1235,
		"0.4895":  490,
		"0.5005":  501,
		"127.000": 127000,
	}
	for input, want := range tests {
		got, err := ParseMilliDecimal(input)
		if err != nil || got != want {
			t.Fatalf("%s: got=%d err=%v want=%d", input, got, err, want)
		}
	}
}

func TestReviewedDOPBoundaryIsElevenMilli(t *testing.T) {
	a, _ := ParseMilliDecimal("0.4895")
	b, _ := ParseMilliDecimal("0.5005")
	if b-a != 11 {
		t.Fatalf("difference=%d want=11", b-a)
	}
}

func TestParseMilliDecimalRejectsInvalidInput(t *testing.T) {
	tests := []string{
		"",
		"-1",
		"1.2.3",
		".5",
		"1.x",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseMilliDecimal(input); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}

func TestParseMilliDecimalRoundsWithCarry(t *testing.T) {
	got, err := ParseMilliDecimal("1.9995")
	if err != nil || got != 2000 {
		t.Fatalf("got=%d err=%v want=2000", got, err)
	}
}

func TestParseMilliDecimalDetectsInt64Overflow(t *testing.T) {
	got, err := ParseMilliDecimal("9223372036854775.8074")
	if err != nil || got != int64(^uint64(0)>>1) {
		t.Fatalf("maximum: got=%d err=%v", got, err)
	}

	for _, input := range []string{
		"9223372036854775.8075",
		"9223372036854776",
		"999999999999999999999999999999999999999999999999",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseMilliDecimal(input); err == nil {
				t.Fatal("expected overflow error")
			}
		})
	}
}
