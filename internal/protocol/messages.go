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

type SwitchType uint8

const (
	TypeGPS       SwitchType = 1
	TypeBeiDou    SwitchType = 2
	TypeGPSBeiDou SwitchType = 3
)

type SwitchResult uint8

const (
	SwitchSuccess           SwitchResult = 0
	SwitchInvalidArgument   SwitchResult = 1
	SwitchForbidden         SwitchResult = 2
	SwitchSerialUnavailable SwitchResult = 3
	SwitchTimeout           SwitchResult = 4
	SwitchVerifyFailed      SwitchResult = 5
	SwitchInternalError     SwitchResult = 6
	SwitchBusy              SwitchResult = 7
)

type SwitchRequest struct {
	RequestID uint32
	Enabled   uint8
	Type      SwitchType
}

type SwitchACK struct {
	RequestID uint32
	Result    SwitchResult
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
	payload[5] = byte(request.Type)
	return frame(TypeSwitchRequest, payload)
}

func EncodeSwitchACK(ack SwitchACK) []byte {
	payload := make([]byte, 5)
	binary.BigEndian.PutUint32(payload[0:4], ack.RequestID)
	payload[4] = byte(ack.Result)
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
		Type:      SwitchType(payload[5]),
	}, nil
}

func ParseSwitchACK(payload []byte) (SwitchACK, error) {
	if len(payload) != 5 {
		return SwitchACK{}, fmt.Errorf("switch ACK payload length %d, want 5", len(payload))
	}
	return SwitchACK{
		RequestID: binary.BigEndian.Uint32(payload[0:4]),
		Result:    SwitchResult(payload[4]),
	}, nil
}
