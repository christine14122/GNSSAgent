//go:build linux

package udpinput

import "os"

func readProcUDPDrops(inode uint64) (uint64, bool) {
	data, err := os.ReadFile("/proc/net/udp")
	if err != nil {
		return 0, false
	}
	return parseProcUDPDrops(string(data), inode)
}
