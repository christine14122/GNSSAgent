package protocol

import (
	"bytes"
	"encoding/binary"
)

var magicBytes = []byte(Magic)

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
	return &Decoder{
		maxPayload: maxPayload,
		buf:        make([]byte, 0, HeaderSize+maxPayload),
	}
}

func (d *Decoder) Feed(input []byte) []Frame {
	var frames []Frame
	for len(input) > 0 {
		room := cap(d.buf) - len(d.buf)
		if room == 0 {
			frames = d.drain(frames)
			room = cap(d.buf) - len(d.buf)
			if room == 0 {
				panic("protocol: decoder full buffer made no progress")
			}
		}
		if room > len(input) {
			room = len(input)
		}
		d.buf = append(d.buf, input[:room]...)
		input = input[room:]
		frames = d.drain(frames)
	}
	return frames
}

func (d *Decoder) drain(frames []Frame) []Frame {
	for {
		magicIndex := bytes.Index(d.buf, magicBytes)
		if magicIndex < 0 {
			d.retainMagicTail()
			return frames
		}
		if magicIndex > 0 {
			d.discard(magicIndex)
		}
		if len(d.buf) < HeaderSize {
			return frames
		}

		payloadLength := int(binary.BigEndian.Uint16(d.buf[6:8]))
		if payloadLength > d.maxPayload {
			d.discard(1)
			continue
		}

		frameLength := HeaderSize + payloadLength
		if len(d.buf) < frameLength {
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
		d.discard(frameLength)
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
	if keep > 0 {
		copy(d.buf[:keep], d.buf[len(d.buf)-keep:])
	}
	d.buf = d.buf[:keep]
}

func (d *Decoder) discard(count int) {
	if count <= 0 {
		return
	}
	copy(d.buf, d.buf[count:])
	d.buf = d.buf[:len(d.buf)-count]
}
