package server

import (
	"encoding/binary"
	"testing"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/protocol"
)

func TestSessionRechecksQueuedTimeQualityAfterWriteLock(t *testing.T) {
	for _, statusType := range []protocol.StatusType{protocol.StatusSimple, protocol.StatusFull} {
		for _, expired := range []bool{false, true} {
			name := "simple/fresh"
			if statusType == protocol.StatusFull {
				name = "full/fresh"
			}
			if expired {
				name = name[:len(name)-5] + "expired"
			}
			t.Run(name, func(t *testing.T) {
				client, session, hub := startPipeSession(t, time.Second, time.Second)
				defer client.Close()
				writeAll(t, client, protocol.EncodeSubscribeRequest(statusType))
				assertACK(t, client, protocol.SubscribeSuccess)

				status := qualityStatus(12345)
				status.TimeQuality.ExpiresAt = time.Now().Add(time.Hour)
				if expired {
					status.TimeQuality.ExpiresAt = time.Now().Add(30 * time.Millisecond)
				}
				session.writeMu.Lock()
				hub.Publish(status)
				if expired {
					<-time.After(time.Until(status.TimeQuality.ExpiresAt))
				}
				session.writeMu.Unlock()

				frame := readFrame(t, client, time.Second)
				offset, size, wantType := 58, 64, protocol.TypeStatusSimple
				if statusType == protocol.StatusFull {
					offset, size, wantType = 124, 136, protocol.TypeStatusFull
				}
				if frame.Version != protocol.Version || frame.Type != wantType || len(frame.Payload) != size {
					t.Fatalf("unexpected status frame: %+v", frame)
				}
				if got := binary.BigEndian.Uint64(frame.Payload[8:16]); got != status.UTCTime {
					t.Fatalf("navigation UTC = %d, want %d", got, status.UTCTime)
				}
				state, reason := byte(2), byte(2)
				if expired {
					state, reason = 0, 9
				}
				if frame.Payload[offset] != state || frame.Payload[offset+1] != reason {
					t.Fatalf("quality state/reason = %v, want %d/%d", frame.Payload[offset:offset+2], state, reason)
				}
				timeoutOffset := offset + 2
				if statusType == protocol.StatusFull {
					timeoutOffset += 2
					rmsValid := binary.BigEndian.Uint64(frame.Payload[:8])&model.FullTimeRMSValid != 0
					samples := binary.BigEndian.Uint16(frame.Payload[126:128])
					rms := binary.BigEndian.Uint32(frame.Payload[132:136])
					if expired && (rmsValid || samples != 0 || rms != 0) {
						t.Fatalf("expired quality retains RMS/samples: %x", frame.Payload)
					}
					if !expired && (!rmsValid || samples != 10 || rms == 0) {
						t.Fatalf("fresh quality lost RMS/samples: %x", frame.Payload)
					}
				}
				if timeout := binary.BigEndian.Uint32(frame.Payload[timeoutOffset:]); timeout != 3000 {
					t.Fatalf("timeout = %d, want 3000", timeout)
				}
				assertReadTimeout(t, client, 10*time.Millisecond)
			})
		}
	}
}

func TestTrustedStatusWriteCannotOutliveTimeQuality(t *testing.T) {
	for _, statusType := range []protocol.StatusType{protocol.StatusSimple, protocol.StatusFull} {
		t.Run(string(rune('0'+statusType)), func(t *testing.T) {
			client, session, hub := startPipeSession(t, time.Second, 3*time.Second)
			defer client.Close()
			writeAll(t, client, protocol.EncodeSubscribeRequest(statusType))
			assertACK(t, client, protocol.SubscribeSuccess)
			status := qualityStatus(12345)
			status.TimeQuality.ExpiresAt = time.Now().Add(50 * time.Millisecond)
			hub.Publish(status)

			// Leave net.Pipe unread so Write cannot finish before the deadline.
			select {
			case <-session.done:
				if time.Now().Before(status.TimeQuality.ExpiresAt) {
					t.Fatal("session closed before the quality deadline")
				}
			case <-time.After(time.Second):
				t.Fatal("blocked trusted write outlived its quality deadline")
			}
		})
	}
}
