package protocol

import (
	"bytes"
	"encoding/binary"
)

type Frame struct {
	Version uint8
	Type    uint8
	Payload []byte
}

type Decoder struct {
	maxPayload int
	buf        []byte
}

func NewDecoder(maxPayload int) *Decoder {
	if maxPayload <= 0 || maxPayload > MaxPayload {
		maxPayload = MaxPayload
	}
	return &Decoder{maxPayload: maxPayload}
}

func (d *Decoder) Feed(input []byte) []Frame {
	if len(input) != 0 {
		d.buf = append(d.buf, input...)
	}

	var frames []Frame
	for {
		magicIndex := bytes.Index(d.buf, []byte(Magic))
		if magicIndex < 0 {
			d.retainMagicTail()
			return frames
		}
		if magicIndex > 0 {
			d.buf = d.buf[magicIndex:]
		}
		if len(d.buf) < HeaderSize {
			d.compact()
			return frames
		}

		payloadLength := int(binary.BigEndian.Uint16(d.buf[6:8]))
		if payloadLength > d.maxPayload {
			d.buf = d.buf[1:]
			continue
		}

		frameLength := HeaderSize + payloadLength
		if len(d.buf) < frameLength {
			d.compact()
			return frames
		}

		version := d.buf[4]
		messageType := d.buf[5]
		if version != Version || validVersionOneLength(messageType, payloadLength) {
			payload := make([]byte, payloadLength)
			copy(payload, d.buf[HeaderSize:frameLength])
			frames = append(frames, Frame{
				Version: version,
				Type:    messageType,
				Payload: payload,
			})
		}
		d.buf = d.buf[frameLength:]
	}
}

func validVersionOneLength(messageType uint8, payloadLength int) bool {
	expected, known := versionOnePayloadLength(messageType)
	return !known || payloadLength == expected
}

func versionOnePayloadLength(messageType uint8) (int, bool) {
	switch messageType {
	case TypeSubscribeRequest:
		return 1, true
	case TypeSubscribeACK:
		return 1, true
	case TypeStatusFull:
		return fullPayloadSize, true
	case TypeStatusSimple:
		return simplePayloadSize, true
	case TypeSwitchRequest:
		return 6, true
	case TypeSwitchACK:
		return 5, true
	default:
		return 0, false
	}
}

func (d *Decoder) retainMagicTail() {
	keep := len(Magic) - 1
	if len(d.buf) < keep {
		keep = len(d.buf)
	}
	if keep == 0 {
		d.buf = nil
		return
	}
	tail := make([]byte, keep)
	copy(tail, d.buf[len(d.buf)-keep:])
	d.buf = tail
}

func (d *Decoder) compact() {
	if len(d.buf) == 0 {
		d.buf = nil
		return
	}
	compact := make([]byte, len(d.buf))
	copy(compact, d.buf)
	d.buf = compact
}
