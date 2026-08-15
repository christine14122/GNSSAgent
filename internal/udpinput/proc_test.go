package udpinput

import "testing"

func TestParseProcUDPDropsMatchesSocketInode(t *testing.T) {
	text := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
  12: 0100007F:733D 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 11111 2 0000000000000000 3
  13: 0100007F:733E 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 22222 2 0000000000000000 17
malformed line
  14: 0100007F:733F 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 not-an-inode 2 0000000000000000 99
`

	got, ok := parseProcUDPDrops(text, 22222)
	if !ok || got != 17 {
		t.Fatalf("parseProcUDPDrops() = (%d, %v), want (17, true)", got, ok)
	}
	if _, ok := parseProcUDPDrops(text, 33333); ok {
		t.Fatal("unexpected match for unrelated inode")
	}
}

func TestParseProcUDPDropsRejectsMalformedMatchingLine(t *testing.T) {
	text := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
  12: 0100007F:733D 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 22222 2 0000000000000000 not-a-number
`
	if _, ok := parseProcUDPDrops(text, 22222); ok {
		t.Fatal("malformed drop count unexpectedly accepted")
	}
}

func TestParseProcUDPDropsRequiresDropsColumn(t *testing.T) {
	text := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
  12: 0100007F:733D 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 22222
`
	if _, ok := parseProcUDPDrops(text, 22222); ok {
		t.Fatal("kernel layout without drops column unexpectedly accepted")
	}
}
