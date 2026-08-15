package regression

import (
	"testing"

	"gnssagent/internal/nmea"
)

func TestFrozenDOPLexicalCarry(t *testing.T) {
	tests := map[string]int64{
		"0.4895": 490,
		"0.5005": 501,
		"0.9995": 1000,
	}
	for input, want := range tests {
		got, err := nmea.ParseMilliDecimal(input)
		if err != nil || got != want {
			t.Fatalf("%s: got %d err=%v want %d", input, got, err, want)
		}
	}
}
