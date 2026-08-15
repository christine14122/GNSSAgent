package udpinput

import (
	"strconv"
	"strings"
)

func parseProcUDPDrops(text string, inode uint64) (uint64, bool) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || !hasField(strings.Fields(lines[0]), "drops") {
		return 0, false
	}

	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 13 {
			continue
		}
		lineInode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil || lineInode != inode {
			continue
		}
		drops, err := strconv.ParseUint(fields[len(fields)-1], 10, 64)
		if err != nil {
			return 0, false
		}
		return drops, true
	}
	return 0, false
}

func hasField(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}
