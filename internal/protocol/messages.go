package protocol

import (
	"encoding/binary"
	"fmt"
)

type StatusType uint8

const (
	StatusSimple StatusType = 1
	StatusFull   StatusType = 2
)

type SubscribeResult uint8

const (
	SubscribeSuccess            SubscribeResult = 0
	SubscribeServerFull         SubscribeResult = 1
	SubscribeAlreadySubscribed  SubscribeResult = 2
	SubscribeUnsupportedVersion SubscribeResult = 3
	SubscribeInternalError      SubscribeResult = 4
	SubscribeInvalidStatusType  SubscribeResult = 5
)

const (
	TypeGPS       uint8 = 1
	TypeBeiDou    uint8 = 2
	TypeGPSBeiDou uint8 = 3
)

const (
	SwitchSuccess           uint8 = 0
	SwitchInvalidArgument   uint8 = 1
	SwitchForbidden         uint8 = 2
	SwitchSerialUnavailable uint8 = 3
	SwitchTimeout           uint8 = 4
	SwitchVerifyFailed      uint8 = 5
	SwitchInternalError     uint8 = 6
	SwitchBusy              uint8 = 7
)

type SwitchRequest struct {
	RequestID uint32
	Enabled   uint8
	Type      uint8
}

type SwitchACK struct {
	RequestID uint32
	Result    uint8
}

func EncodeSubscribeRequest(statusType StatusType) []byte {
	return frame(TypeSubscribeRequest, []byte{byte(statusType)})
}

func EncodeSubscribeACK(result SubscribeResult) []byte {
	return frame(TypeSubscribeACK, []byte{byte(result)})
}

func EncodeSwitchRequest(request SwitchRequest) []byte {
	payload := make([]byte, 6)
	binary.BigEndian.PutUint32(payload[0:4], request.RequestID)
	payload[4] = request.Enabled
	payload[5] = request.Type
	return frame(TypeSwitchRequest, payload)
}

func EncodeSwitchACK(ack SwitchACK) []byte {
	payload := make([]byte, 5)
	binary.BigEndian.PutUint32(payload[0:4], ack.RequestID)
	payload[4] = ack.Result
	return frame(TypeSwitchACK, payload)
}

func ParseSubscribeRequest(payload []byte) (StatusType, error) {
	if len(payload) != 1 {
		return 0, fmt.Errorf("subscribe request payload length %d, want 1", len(payload))
	}
	return StatusType(payload[0]), nil
}

func ParseSubscribeACK(payload []byte) (SubscribeResult, error) {
	if len(payload) != 1 {
		return 0, fmt.Errorf("subscribe ACK payload length %d, want 1", len(payload))
	}
	return SubscribeResult(payload[0]), nil
}

func ParseSwitchRequest(payload []byte) (SwitchRequest, error) {
	if len(payload) != 6 {
		return SwitchRequest{}, fmt.Errorf("switch request payload length %d, want 6", len(payload))
	}
	return SwitchRequest{
		RequestID: binary.BigEndian.Uint32(payload[0:4]),
		Enabled:   payload[4],
		Type:      payload[5],
	}, nil
}

func ParseSwitchACK(payload []byte) (SwitchACK, error) {
	if len(payload) != 5 {
		return SwitchACK{}, fmt.Errorf("switch ACK payload length %d, want 5", len(payload))
	}
	return SwitchACK{
		RequestID: binary.BigEndian.Uint32(payload[0:4]),
		Result:    payload[4],
	}, nil
}
