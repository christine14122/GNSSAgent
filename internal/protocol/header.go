package protocol

import (
	"encoding/binary"
	"fmt"
)

const (
	Magic            = "GNSS"
	Version1   uint8 = 1
	Version2   uint8 = 2
	Version          = Version2
	HeaderSize       = 8
	MaxPayload       = 1024
)

const (
	TypeSubscribeRequest uint8 = 0x01
	TypeSubscribeACK     uint8 = 0x02
	TypeStatusFull       uint8 = 0x03
	TypeStatusSimple     uint8 = 0x04
	TypeSwitchRequest    uint8 = 0x10
	TypeSwitchACK        uint8 = 0x11
)

func frame(messageType uint8, payload []byte) []byte {
	if len(payload) > MaxPayload {
		panic(fmt.Sprintf("protocol: payload length %d exceeds maximum %d", len(payload), MaxPayload))
	}
	out := make([]byte, HeaderSize+len(payload))
	copy(out[:4], Magic)
	out[4] = Version
	out[5] = messageType
	binary.BigEndian.PutUint16(out[6:8], uint16(len(payload)))
	copy(out[HeaderSize:], payload)
	return out
}
