package server

import (
	"testing"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/protocol"
)

func TestSessionPublishesEmbeddedTimeQualityOnly(t *testing.T) {
	for _, statusType := range []protocol.StatusType{protocol.StatusSimple, protocol.StatusFull} {
		t.Run(string(rune('0'+statusType)), func(t *testing.T) {
			client, _, hub := startPipeSession(t, time.Second, time.Second)
			defer client.Close()
			writeAll(t, client, protocol.EncodeSubscribeRequest(statusType))
			assertACK(t, client, protocol.SubscribeSuccess)
			hub.Publish(qualityStatus(12345))
			status := readFrame(t, client, time.Second)
			wantType, size, offset := protocol.TypeStatusSimple, 64, 58
			if statusType == protocol.StatusFull {
				wantType, size, offset = protocol.TypeStatusFull, 136, 124
			}
			if status.Version != 2 || status.Type != wantType || len(status.Payload) != size {
				t.Fatalf("status=%+v", status)
			}
			if status.Payload[offset] != 2 || status.Payload[offset+1] != 2 {
				t.Fatalf("embedded quality=%x", status.Payload[offset:])
			}
			assertReadTimeout(t, client, 20*time.Millisecond)
		})
	}
}

func TestHubReplacesNavigationAndQualityInOneFrame(t *testing.T) {
	hub := NewHub()
	for _, statusType := range []protocol.StatusType{protocol.StatusSimple, protocol.StatusFull} {
		subscriber := newSubscriber(statusType)
		hub.add(subscriber)
		defer hub.remove(subscriber)
	}
	hub.Publish(qualityStatus(1000))
	latest := qualityStatus(2000)
	latest.TimeQuality.State, latest.TimeQuality.Reason = 3, 3
	hub.Publish(latest)
	for subscriber := range hub.subscribers {
		status := <-subscriber.queue
		if status != latest {
			t.Fatalf("mixed navigation and quality: %+v", status)
		}
	}
}

func TestSessionRejectsVersionOneSubscription(t *testing.T) {
	client, session, _ := startPipeSession(t, time.Second, time.Second)
	defer client.Close()
	writeAll(t, client, rawFrame(1, protocol.TypeSubscribeRequest, []byte{byte(protocol.StatusSimple)}))
	assertACK(t, client, protocol.SubscribeUnsupportedVersion)
	if session.isSubscribed() {
		t.Fatal("v1 subscription was accepted")
	}
	writeAll(t, client, protocol.EncodeSubscribeRequest(protocol.StatusSimple))
	assertACK(t, client, protocol.SubscribeSuccess)
}

func qualityStatus(utc uint64) model.FullStatus {
	return model.FullStatus{
		FieldValidityMask: model.FullUTCValid | model.FullRecvValid,
		UTCTime:           utc, RecvTime: utc + 20,
		TimeQuality: model.TimeQuality{
			Evaluated: true, State: 2, Reason: 2, Samples: 10,
			TimeoutMillis: 3000, RMSValid: true, RMS: 1.5,
		},
	}
}
