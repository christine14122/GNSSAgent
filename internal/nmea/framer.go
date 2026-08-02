package nmea

import (
	"bytes"
	"errors"
	"fmt"
)

type Framer struct {
	max        int
	buf        []byte
	discarding bool
}

func NewFramer(max int) *Framer {
	return &Framer{max: max}
}

func (f *Framer) Feed(data []byte) [][]byte {
	var frames [][]byte
	for _, b := range data {
		if b == '$' {
			f.buf = append(f.buf[:0], b)
			f.discarding = len(f.buf) > f.max
			if f.discarding {
				f.buf = f.buf[:0]
			}
			continue
		}

		if f.discarding || len(f.buf) == 0 {
			continue
		}

		if b == '\n' {
			end := len(f.buf)
			if end > 0 && f.buf[end-1] == '\r' {
				end--
			}
			frames = append(frames, append([]byte(nil), f.buf[:end]...))
			f.buf = f.buf[:0]
			continue
		}

		f.buf = append(f.buf, b)
		if len(f.buf) > f.max {
			f.buf = f.buf[:0]
			f.discarding = true
		}
	}
	return frames
}

func ValidateChecksum(sentence []byte) error {
	if len(sentence) == 0 || sentence[0] != '$' {
		return errors.New("invalid NMEA framing: sentence must start with '$'")
	}

	star := bytes.LastIndexByte(sentence, '*')
	if star < 0 {
		return errors.New("missing NMEA checksum: expected *HH at end")
	}
	if star != len(sentence)-3 || bytes.IndexByte(sentence[1:star], '*') >= 0 {
		return errors.New("malformed NMEA checksum: expected exactly *HH at end")
	}

	high, ok := hexDigit(sentence[star+1])
	if !ok {
		return fmt.Errorf("malformed NMEA checksum: %q is not hexadecimal", sentence[star+1])
	}
	low, ok := hexDigit(sentence[star+2])
	if !ok {
		return fmt.Errorf("malformed NMEA checksum: %q is not hexadecimal", sentence[star+2])
	}

	var actual byte
	for _, b := range sentence[1:star] {
		actual ^= b
	}
	expected := high<<4 | low
	if actual != expected {
		return fmt.Errorf("NMEA checksum mismatch: computed %02X, sentence has %02X", actual, expected)
	}
	return nil
}

func hexDigit(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}
