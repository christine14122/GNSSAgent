//go:build linux

package server

import (
	"net"

	"golang.org/x/sys/unix"
)

func tryReadBuffered(conn net.Conn, buffer []byte) (int, bool) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return 0, false
	}
	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return 0, false
	}
	var n int
	var recvErr error
	if err := rawConn.Control(func(fd uintptr) {
		n, _, recvErr = unix.Recvfrom(int(fd), buffer, unix.MSG_DONTWAIT)
	}); err != nil || recvErr != nil || n == 0 {
		return 0, false
	}
	return n, true
}
