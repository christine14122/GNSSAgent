package udpinput

import "bytes"

const (
	MaxDatagramSize = 1024
	ReadBufferSize  = 256 * 1024
)

type RejectReason uint8

const (
	RejectNone RejectReason = iota
	RejectEmpty
	RejectStart
	RejectTooLong
	RejectTruncated
	RejectNUL
	RejectMultiple
	RejectTerminator
	RejectSource
)

func NormalizeDatagram(buffer []byte, n int, truncated bool) ([]byte, RejectReason) {
	if n < 0 || n > len(buffer) || n == 0 {
		return nil, RejectEmpty
	}
	if truncated {
		return nil, RejectTruncated
	}
	if n > MaxDatagramSize {
		return nil, RejectTooLong
	}

	datagram := buffer[:n]
	if datagram[0] != '$' {
		return nil, RejectStart
	}
	if bytes.IndexByte(datagram, 0) >= 0 {
		return nil, RejectNUL
	}
	if bytes.Count(datagram, []byte{'$'}) != 1 {
		return nil, RejectMultiple
	}

	switch {
	case bytes.HasSuffix(datagram, []byte("\r\n")):
		datagram = datagram[:len(datagram)-2]
	case bytes.HasSuffix(datagram, []byte("\n")):
		datagram = datagram[:len(datagram)-1]
	}
	if bytes.IndexAny(datagram, "\r\n") >= 0 {
		return nil, RejectTerminator
	}

	return append([]byte(nil), datagram...), RejectNone
}
