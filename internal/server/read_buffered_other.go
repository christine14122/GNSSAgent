//go:build !linux

package server

import "net"

func tryReadBuffered(net.Conn, []byte) (int, bool) {
	return 0, false
}
