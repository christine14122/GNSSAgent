package observe

import (
	"sync"
	"testing"
	"time"

	"gnssagent/internal/nmea"
	"gnssagent/internal/udpinput"
)

func TestStatsSnapshotAndReset(t *testing.T) {
	stats := NewStats()
	receivedAt := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	stats.RecordDatagram(42)
	stats.RecordDatagram(18)
	stats.RecordUDPReject(udpinput.RejectNUL)
	stats.RecordNMEAReject(true)
	stats.RecordNMEAReject(false)
	stats.RecordValidNMEA("GN", nmea.KindGGA, receivedAt)
	stats.RecordKernelDrops(3, udpinput.DropRXQOverflow)
	stats.RecordGSVComplete()
	stats.RecordGSVIncomplete()
	stats.RecordPublishedCycle()
	stats.RecordTCPConnection()
	stats.RecordTCPRejection()
	stats.RecordTCPSubscription()
	stats.RecordSlowClientReplacement()

	got := stats.SnapshotReset()
	if got.UDPDatagrams != 2 || got.UDPBytes != 60 {
		t.Fatalf("UDP totals = %d/%d", got.UDPDatagrams, got.UDPBytes)
	}
	if got.UDPRejects[udpinput.RejectNUL] != 1 || got.ChecksumFailures != 1 || got.ParserFailures != 1 {
		t.Fatalf("reject counters = %#v checksum=%d parser=%d", got.UDPRejects, got.ChecksumFailures, got.ParserFailures)
	}
	if got.ValidNMEA[NMEAKey{Talker: "GN", Kind: nmea.KindGGA}] != 1 || !got.LastValidNMEA.Equal(receivedAt) {
		t.Fatalf("valid NMEA = %#v last=%v", got.ValidNMEA, got.LastValidNMEA)
	}
	if got.KernelDrops != 3 || got.KernelDropsBySource[udpinput.DropRXQOverflow] != 3 {
		t.Fatalf("kernel drops = %d %#v", got.KernelDrops, got.KernelDropsBySource)
	}
	if got.GSVComplete != 1 || got.GSVIncomplete != 1 || got.PublishedCycles != 1 {
		t.Fatalf("cycle counters = %+v", got)
	}
	if got.TCPConnections != 1 || got.TCPRejections != 1 || got.TCPSubscriptions != 1 || got.SlowClientReplacements != 1 {
		t.Fatalf("TCP counters = %+v", got)
	}

	empty := stats.SnapshotReset()
	if empty.UDPDatagrams != 0 || empty.PublishedCycles != 0 || len(empty.ValidNMEA) != 0 {
		t.Fatalf("interval counters did not reset: %+v", empty)
	}
	if !empty.LastValidNMEA.Equal(receivedAt) {
		t.Fatalf("last-valid gauge reset: %v", empty.LastValidNMEA)
	}
}

func TestSnapshotAccumulatePreservesAllBuckets(t *testing.T) {
	first := Snapshot{
		UDPDatagrams:        2,
		UDPBytes:            20,
		UDPRejects:          map[udpinput.RejectReason]uint64{udpinput.RejectNUL: 1},
		ValidNMEA:           map[NMEAKey]uint64{{Talker: "GP", Kind: nmea.KindGGA}: 2},
		KernelDrops:         3,
		KernelDropsBySource: map[udpinput.DropSource]uint64{udpinput.DropProcUDP: 3},
		LastValidNMEA:       time.Unix(10, 0),
	}
	second := Snapshot{
		UDPDatagrams:        4,
		UDPBytes:            40,
		UDPRejects:          map[udpinput.RejectReason]uint64{udpinput.RejectStart: 2},
		ValidNMEA:           map[NMEAKey]uint64{{Talker: "BD", Kind: nmea.KindGSV}: 4},
		KernelDrops:         5,
		KernelDropsBySource: map[udpinput.DropSource]uint64{udpinput.DropRXQOverflow: 5},
		LastValidNMEA:       time.Unix(20, 0),
	}

	var total Snapshot
	total.Add(first)
	total.Add(second)
	if total.UDPDatagrams != 6 || total.UDPBytes != 60 || total.KernelDrops != 8 {
		t.Fatalf("totals = %+v", total)
	}
	if total.UDPRejects[udpinput.RejectNUL] != 1 || total.UDPRejects[udpinput.RejectStart] != 2 {
		t.Fatalf("reject buckets = %#v", total.UDPRejects)
	}
	if total.ValidNMEA[NMEAKey{Talker: "GP", Kind: nmea.KindGGA}] != 2 || total.ValidNMEA[NMEAKey{Talker: "BD", Kind: nmea.KindGSV}] != 4 {
		t.Fatalf("NMEA buckets = %#v", total.ValidNMEA)
	}
	if !total.LastValidNMEA.Equal(second.LastValidNMEA) {
		t.Fatalf("last valid = %v", total.LastValidNMEA)
	}
}

func TestStatsConcurrentUpdatesAreNotLost(t *testing.T) {
	stats := NewStats()
	const (
		workers    = 32
		iterations = 250
	)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				stats.RecordDatagram(10)
				stats.RecordValidNMEA("GN", nmea.KindRMC, time.Unix(int64(i), 0))
			}
		}()
	}
	wg.Wait()

	got := stats.SnapshotReset()
	want := uint64(workers * iterations)
	if got.UDPDatagrams != want || got.UDPBytes != want*10 || got.ValidNMEA[NMEAKey{Talker: "GN", Kind: nmea.KindRMC}] != want {
		t.Fatalf("concurrent totals = %+v, want %d events", got, want)
	}
}
