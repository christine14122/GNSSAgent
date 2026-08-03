package nmea

import (
	"bytes"
	"testing"
)

func TestFramerHandlesFragmentsNoiseAndMultipleLines(t *testing.T) {
	f := NewFramer(1024)
	if got := f.Feed([]byte("noise$GNRM")); len(got) != 0 {
		t.Fatalf("unexpected frame: %q", got)
	}
	got := f.Feed([]byte("C,1*00\r\n$GPGSV,1,1,00*79\n"))
	if len(got) != 2 || string(got[0]) != "$GNRMC,1*00" || string(got[1]) != "$GPGSV,1,1,00*79" {
		t.Fatalf("frames=%q", got)
	}
}

func TestFramerDropsOverlongLineAndResynchronizes(t *testing.T) {
	f := NewFramer(16)
	got := f.Feed([]byte("$12345678901234567$X*00\n"))
	if len(got) != 1 || string(got[0]) != "$X*00" {
		t.Fatalf("frames=%q", got)
	}
}

func TestFramerOutputRemainsOwnedAcrossLaterFeeds(t *testing.T) {
	f := NewFramer(32)
	got := f.Feed([]byte("$A*00\n"))
	if len(got) != 1 {
		t.Fatalf("frames=%q", got)
	}

	want := []byte("$A*00")
	f.Feed([]byte("$LONGER*00\n"))
	if !bytes.Equal(got[0], want) {
		t.Fatalf("frame changed after later Feed: got=%q want=%q", got[0], want)
	}
}

func TestFramerDiscardsOverlongCandidateUntilNextDollar(t *testing.T) {
	f := NewFramer(5)
	if got := f.Feed([]byte("$12345")); len(got) != 0 {
		t.Fatalf("unexpected frame while overflowing: %q", got)
	}
	if got := f.Feed([]byte("\n")); len(got) != 0 {
		t.Fatalf("unexpected frame at discarded LF: %q", got)
	}
	got := f.Feed([]byte("noise$X\n"))
	if len(got) != 1 || string(got[0]) != "$X" {
		t.Fatalf("frames=%q", got)
	}
}

func TestFramerAcceptsExactMaximumLengthWithLF(t *testing.T) {
	f := NewFramer(5)
	got := f.Feed([]byte("$1234\n"))
	if len(got) != 1 || string(got[0]) != "$1234" {
		t.Fatalf("frames=%q", got)
	}
}

func TestFramerAcceptsExactMaximumLengthWithCRLF(t *testing.T) {
	f := NewFramer(5)
	got := f.Feed([]byte("$1234\r\n"))
	if len(got) != 1 || string(got[0]) != "$1234" {
		t.Fatalf("frames=%q", got)
	}
}

func TestFramerAcceptsExactMaximumLengthWithSplitCRLF(t *testing.T) {
	f := NewFramer(5)
	if got := f.Feed([]byte("$1234\r")); len(got) != 0 {
		t.Fatalf("unexpected frame before LF: %q", got)
	}
	got := f.Feed([]byte("\n"))
	if len(got) != 1 || string(got[0]) != "$1234" {
		t.Fatalf("frames=%q", got)
	}
}

func TestFramerDropsMaximumLengthPlusOne(t *testing.T) {
	tests := []struct {
		name       string
		terminator string
	}{
		{name: "LF", terminator: "\n"},
		{name: "CRLF", terminator: "\r\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := NewFramer(5)
			if got := f.Feed([]byte("$12345" + test.terminator)); len(got) != 0 {
				t.Fatalf("unexpected frame: %q", got)
			}
		})
	}
}

func TestFramerDropsPendingCRFollowedByNonLFAndRecovers(t *testing.T) {
	f := NewFramer(5)
	if got := f.Feed([]byte("$1234\rX\n")); len(got) != 0 {
		t.Fatalf("unexpected overlong frame: %q", got)
	}
	got := f.Feed([]byte("noise$A\n"))
	if len(got) != 1 || string(got[0]) != "$A" {
		t.Fatalf("frames=%q", got)
	}
}

func TestValidateChecksum(t *testing.T) {
	if err := ValidateChecksum([]byte("$GPGSV,1,1,00*79")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateChecksum([]byte("$GPGSV,1,1,00*78")); err == nil {
		t.Fatal("expected checksum error")
	}
}

func TestValidateChecksumRejectsMalformedInput(t *testing.T) {
	tests := map[string]string{
		"missing framing":          "GPGSV,1,1,00*79",
		"missing star":             "$GPGSV,1,1,00",
		"bad hexadecimal digit":    "$GPGSV,1,1,00*7Z",
		"one checksum digit":       "$GPGSV,1,1,00*7",
		"three checksum digits":    "$GPGSV,1,1,00*799",
		"trailing checksum suffix": "$GPGSV,1,1,00*79x",
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateChecksum([]byte(input)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
