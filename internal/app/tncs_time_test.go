package app

import (
	"context"
	"testing"
	"time"

	"gnssagent/internal/nmea"
)

type fakeTimeReporter struct {
	started   chan struct{}
	stopped   chan struct{}
	sentences chan nmea.Sentence
}

func (f *fakeTimeReporter) Run(ctx context.Context) {
	close(f.started)
	<-ctx.Done()
	close(f.stopped)
}
func (f *fakeTimeReporter) Sentence(s nmea.Sentence) { f.sentences <- s }

func TestTimeReporterReceivesSentenceBeforeAggregateFlushAndStopsWithApp(t *testing.T) {
	input, server := newFakeUDPInput(), newFakeStatusServer()
	a := New(input, server, nil, nil)
	reporter := &fakeTimeReporter{make(chan struct{}), make(chan struct{}), make(chan nmea.Sentence, 1)}
	a.SetTimeReporter(reporter)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer cancel()
	var sink *inputSink
	select {
	case s := <-input.started:
		sink = s.(*inputSink)
	case <-time.After(time.Second):
		t.Fatal("input did not start")
	}
	sentence := nmea.Sentence{Kind: nmea.KindRMC, ReceivedAt: time.Now(), RMC: &nmea.RMC{TimeValid: true, MillisOfDay: 1000}}
	sink.Sentence(sentence)
	select {
	case got := <-reporter.sentences:
		if got.RMC != sentence.RMC {
			t.Fatal("sentence changed before reporter")
		}
	default:
		t.Fatal("reporter waited for aggregation")
	}
	select {
	case <-server.published:
		t.Fatal("aggregation unexpectedly already flushed")
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("app failed to stop")
	}
	select {
	case <-reporter.stopped:
	default:
		t.Fatal("app did not join reporter")
	}
}
