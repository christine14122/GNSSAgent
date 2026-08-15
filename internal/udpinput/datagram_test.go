package udpinput

import "testing"

func TestNormalizeDatagram(t *testing.T) {
	valid := []struct {
		name string
		data []byte
		want string
	}{
		{"no terminator", []byte("$GPGSV,1,1,00*79"), "$GPGSV,1,1,00*79"},
		{"LF", []byte("$GPGSV,1,1,00*79\n"), "$GPGSV,1,1,00*79"},
		{"CRLF", []byte("$GPGSV,1,1,00*79\r\n"), "$GPGSV,1,1,00*79"},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := NormalizeDatagram(tc.data, len(tc.data), false)
			if reason != RejectNone || string(got) != tc.want {
				t.Fatalf("got %q reason=%v", got, reason)
			}
			if len(got) != 0 && &got[0] == &tc.data[0] {
				t.Fatal("NormalizeDatagram returned the receive buffer instead of an owned copy")
			}
		})
	}
}

func TestNormalizeDatagramRejectsInvalidBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		n         int
		truncated bool
		reason    RejectReason
	}{
		{"negative length", []byte("$A*00"), -1, false, RejectEmpty},
		{"length beyond buffer", []byte("$A*00"), 6, false, RejectEmpty},
		{"empty", nil, 0, false, RejectEmpty},
		{"not dollar", []byte("GPGSV*00"), len("GPGSV*00"), false, RejectStart},
		{"tail NUL", []byte{'$', 'X', '*', '0', '0', 0}, 6, false, RejectNUL},
		{"multiple LF sentences", []byte("$A*00\n$B*00"), 11, false, RejectMultiple},
		{"multiple dollar sentences", []byte("$A*00$B*00"), 10, false, RejectMultiple},
		{"bare CR", []byte("$A*00\r"), 6, false, RejectTerminator},
		{"embedded LF", []byte("$A*00\nTAIL"), 10, false, RejectTerminator},
		{"truncated", []byte("$A*00"), 5, true, RejectTruncated},
		{"overlong", make([]byte, MaxDatagramSize+1), MaxDatagramSize + 1, false, RejectTooLong},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := NormalizeDatagram(tc.data, tc.n, tc.truncated); got != tc.reason {
				t.Fatalf("reason=%v want=%v", got, tc.reason)
			}
		})
	}
}

func TestNormalizeUsesOnlyReturnedLength(t *testing.T) {
	buf := append([]byte("$GPGSV,1,1,00*79"), []byte("\x00$STALE*00")...)
	got, reason := NormalizeDatagram(buf, len("$GPGSV,1,1,00*79"), false)
	if reason != RejectNone || string(got) != "$GPGSV,1,1,00*79" {
		t.Fatalf("got %q reason=%v", got, reason)
	}
}

func TestNormalizeNeverJoinsSplitDatagrams(t *testing.T) {
	first := []byte("$GPGSV,1,1")
	second := []byte(",00*79")

	if got, reason := NormalizeDatagram(first, len(first), false); reason != RejectNone || string(got) != string(first) {
		t.Fatalf("first half got %q reason=%v", got, reason)
	}
	if _, reason := NormalizeDatagram(second, len(second), false); reason != RejectStart {
		t.Fatalf("second half reason=%v want=%v", reason, RejectStart)
	}
}
