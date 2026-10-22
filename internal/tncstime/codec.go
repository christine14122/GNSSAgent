package tncstime

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
	"unicode/utf8"
)

const (
	setTimeRequest  = 101008
	setTimeResponse = 201008
	maxFrameSize    = 64 * 1024
)

type rejection struct{ reason string }

func (e *rejection) Error() string { return "TNCS rejected time: " + e.reason }

// These small proto3 messages use varint and length-delimited fields only.
// Keep the wire schema alongside the codec in set_local_time.proto.
func encodeRequest(req request) []byte {
	var payload []byte
	if req.timestamp != 0 {
		payload = binary.AppendUvarint(append(payload, 0x08), uint64(req.timestamp))
	}
	if req.trusted {
		payload = append(payload, 0x10, 0x01)
	}
	msg := binary.AppendUvarint([]byte{0x08}, setTimeRequest)
	if len(payload) != 0 {
		msg = binary.AppendUvarint(append(msg, 0x12), uint64(len(payload)))
		msg = append(msg, payload...)
	}
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(msg)))
	return append(frame, msg...)
}

func exchange(ctx context.Context, conn net.Conn, req request, sent func()) error {
	// Interrupt blocked reads and writes promptly on shutdown.
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()); close(stopped) })
	defer func() {
		if !stop() {
			<-stopped
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(ioTimeout)); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	frame := encodeRequest(req)
	for len(frame) > 0 {
		n, err := conn.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	if sent != nil {
		sent()
	}
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > maxFrameSize {
		return fmt.Errorf("invalid TNCS frame size %d", length)
	}
	data := make([]byte, int(length))
	if _, err := io.ReadFull(conn, data); err != nil {
		return err
	}
	return decodeResponse(data)
}

type field struct {
	number  uint64
	wire    uint64
	integer uint64
	bytes   []byte
}

// Consume unknown proto3 fields too, so adding fields on TNCS does not break us.
func consume(data []byte) (field, []byte, error) {
	var f field
	tag, n := binary.Uvarint(data)
	if n <= 0 || tag>>3 == 0 || tag>>3 > (1<<29)-1 {
		return f, nil, errors.New("invalid protobuf tag")
	}
	f.number, f.wire = tag>>3, tag&7
	data = data[n:]
	switch f.wire {
	case 0:
		value, count := binary.Uvarint(data)
		if count <= 0 {
			return f, nil, errors.New("invalid protobuf varint")
		}
		f.integer = value
		data = data[count:]
	case 1, 5:
		size := 8
		if f.wire == 5 {
			size = 4
		}
		if len(data) < size {
			return f, nil, io.ErrUnexpectedEOF
		}
		data = data[size:]
	case 2:
		size, count := binary.Uvarint(data)
		if count <= 0 {
			return f, nil, errors.New("invalid protobuf length")
		}
		data = data[count:]
		if size > uint64(len(data)) {
			return f, nil, io.ErrUnexpectedEOF
		}
		f.bytes, data = data[:int(size)], data[int(size):]
	default:
		return f, nil, fmt.Errorf("unsupported protobuf wire type %d", f.wire)
	}
	return f, data, nil
}

func decodeResponse(data []byte) error {
	var messageType uint64
	var payload []byte
	for len(data) > 0 {
		f, rest, err := consume(data)
		if err != nil {
			return err
		}
		data = rest
		switch f.number {
		case 1:
			if f.wire != 0 {
				return errors.New("invalid TNCS msg_type wire type")
			}
			messageType = f.integer
		case 2:
			if f.wire != 2 {
				return errors.New("invalid TNCS payload wire type")
			}
			payload = f.bytes
		}
	}
	if messageType != setTimeResponse {
		return fmt.Errorf("unexpected TNCS message type %d", messageType)
	}
	success, reason := false, ""
	for len(payload) > 0 {
		f, rest, err := consume(payload)
		if err != nil {
			return err
		}
		payload = rest
		switch f.number {
		case 1:
			if f.wire != 0 {
				return errors.New("invalid TNCS success wire type")
			}
			success = f.integer != 0
		case 2:
			if f.wire != 2 || !utf8.Valid(f.bytes) {
				return errors.New("invalid TNCS reason")
			}
			reason = string(f.bytes)
		}
	}
	if !success {
		return &rejection{reason: reason}
	}
	return nil
}
