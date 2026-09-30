package app

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"gnssagent/internal/protocol"
	"gnssagent/internal/timequality"
)

func TestUDPToTCPPublishesRMSConvergenceForBothSubscriptions(t *testing.T) {
	for _, kind := range []protocol.StatusType{protocol.StatusSimple, protocol.StatusFull} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			cfg := timequality.DefaultConfig()
			cfg.Window = 2
			// Send synthetic epochs rapidly; time continuity itself has separate clock tests.
			cfg.MaxTimeStepError = 2 * time.Second
			h := startIntegrationHarness(t, cfg)
			defer h.stop(t)
			client := h.dialTCP(t)
			defer client.Close()
			writeWire(t, client, protocol.EncodeSubscribeRequest(kind))
			ack := readWireFrame(t, client, time.Second)
			if ack[5] != protocol.TypeSubscribeACK || ack[8] != 0 {
				t.Fatalf("ack=%x", ack)
			}
			sender := h.udpSender(t)
			defer sender.Close()
			sendRMC := func(i int) {
				sendDatagram(t, sender, nmeaFrame(fmt.Sprintf("GNRMC,1200%02d.000,A,3156.0,N,11838.0,E,0,0,300926,,,A", i)))
			}
			sendGST := func(i int) {
				sendDatagram(t, sender, nmeaFrame(fmt.Sprintf("GNGST,1200%02d.000,1.0,1.0,1.0,0,1.0,1.0,1.0", i)))
			}
			sendRMC(0)
			sendGST(0)
			for i := 1; i <= 2; i++ {
				sendRMC(i)
				status := readWireFrame(t, client, time.Second)
				wantStatus := protocol.TypeStatusSimple
				wantSize, qualityOffset, timeoutOffset := 72, 66, 68
				if kind == protocol.StatusFull {
					wantStatus = protocol.TypeStatusFull
					wantSize, qualityOffset, timeoutOffset = 144, 132, 136
				}
				if status[4] != 2 || status[5] != wantStatus || len(status) != wantSize {
					t.Fatalf("status=%x", status)
				}
				wantState := uint8(timequality.Warming)
				if i == 2 {
					wantState = uint8(timequality.Trusted)
				}
				if status[qualityOffset] != wantState || binary.BigEndian.Uint32(status[timeoutOffset:timeoutOffset+4]) != 3000 {
					t.Fatalf("embedded quality=%x", status[qualityOffset:])
				}
				if kind == protocol.StatusFull && binary.BigEndian.Uint16(status[134:136]) != uint16(i) {
					t.Fatalf("full sample count=%d", binary.BigEndian.Uint16(status[134:136]))
				}
				assertWireTimeout(t, client, 20*time.Millisecond)
				sendGST(i)
			}
		})
	}
}
