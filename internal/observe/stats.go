package observe

import (
	"sync"
	"time"

	"gnssagent/internal/nmea"
	"gnssagent/internal/protocol"
	"gnssagent/internal/udpinput"
)

type NMEAKey struct {
	Talker string
	Kind   nmea.Kind
}

type Snapshot struct {
	UDPDatagrams           uint64
	UDPBytes               uint64
	UDPRejects             map[udpinput.RejectReason]uint64
	ChecksumFailures       uint64
	ParserFailures         uint64
	ValidNMEA              map[NMEAKey]uint64
	KernelDrops            uint64
	KernelDropsBySource    map[udpinput.DropSource]uint64
	GSVComplete            uint64
	GSVIncomplete          uint64
	PublishedCycles        uint64
	TCPConnections         uint64
	TCPDisconnections      uint64
	TCPRejections          uint64
	TCPSubscriptions       uint64
	TCPSimpleSubscriptions uint64
	TCPFullSubscriptions   uint64
	SlowClientReplacements uint64
	LastValidNMEA          time.Time
}

func (s *Snapshot) Add(other Snapshot) {
	s.UDPDatagrams += other.UDPDatagrams
	s.UDPBytes += other.UDPBytes
	s.ChecksumFailures += other.ChecksumFailures
	s.ParserFailures += other.ParserFailures
	s.KernelDrops += other.KernelDrops
	s.GSVComplete += other.GSVComplete
	s.GSVIncomplete += other.GSVIncomplete
	s.PublishedCycles += other.PublishedCycles
	s.TCPConnections += other.TCPConnections
	s.TCPDisconnections += other.TCPDisconnections
	s.TCPRejections += other.TCPRejections
	s.TCPSubscriptions += other.TCPSubscriptions
	s.TCPSimpleSubscriptions += other.TCPSimpleSubscriptions
	s.TCPFullSubscriptions += other.TCPFullSubscriptions
	s.SlowClientReplacements += other.SlowClientReplacements
	if other.LastValidNMEA.After(s.LastValidNMEA) {
		s.LastValidNMEA = other.LastValidNMEA
	}
	if s.UDPRejects == nil {
		s.UDPRejects = make(map[udpinput.RejectReason]uint64)
	}
	for reason, count := range other.UDPRejects {
		s.UDPRejects[reason] += count
	}
	if s.ValidNMEA == nil {
		s.ValidNMEA = make(map[NMEAKey]uint64)
	}
	for key, count := range other.ValidNMEA {
		s.ValidNMEA[key] += count
	}
	if s.KernelDropsBySource == nil {
		s.KernelDropsBySource = make(map[udpinput.DropSource]uint64)
	}
	for source, count := range other.KernelDropsBySource {
		s.KernelDropsBySource[source] += count
	}
}

type Stats struct {
	mu            sync.Mutex
	interval      Snapshot
	lastValidNMEA time.Time
}

func NewStats() *Stats {
	return &Stats{interval: newSnapshot()}
}

func newSnapshot() Snapshot {
	return Snapshot{
		UDPRejects:          make(map[udpinput.RejectReason]uint64),
		ValidNMEA:           make(map[NMEAKey]uint64),
		KernelDropsBySource: make(map[udpinput.DropSource]uint64),
	}
}

func (s *Stats) RecordDatagram(bytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval.UDPDatagrams++
	if bytes > 0 {
		s.interval.UDPBytes += uint64(bytes)
	}
}

func (s *Stats) RecordUDPReject(reason udpinput.RejectReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval.UDPRejects[reason]++
}

func (s *Stats) RecordNMEAReject(checksum bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if checksum {
		s.interval.ChecksumFailures++
	} else {
		s.interval.ParserFailures++
	}
}

func (s *Stats) RecordValidNMEA(talker string, kind nmea.Kind, receivedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval.ValidNMEA[NMEAKey{Talker: talker, Kind: kind}]++
	if receivedAt.After(s.lastValidNMEA) {
		s.lastValidNMEA = receivedAt
	}
}

func (s *Stats) RecordKernelDrops(delta uint64, source udpinput.DropSource) {
	if delta == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval.KernelDrops += delta
	s.interval.KernelDropsBySource[source] += delta
}

func (s *Stats) RecordGSVComplete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval.GSVComplete++
}

func (s *Stats) RecordGSVIncomplete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval.GSVIncomplete++
}

func (s *Stats) RecordPublishedCycle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval.PublishedCycles++
}

func (s *Stats) RecordTCPConnection() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval.TCPConnections++
}

func (s *Stats) RecordTCPRejection() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval.TCPRejections++
}

func (s *Stats) RecordTCPDisconnection() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval.TCPDisconnections++
}

func (s *Stats) RecordTCPSubscription(statusType protocol.StatusType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch statusType {
	case protocol.StatusSimple:
		s.interval.TCPSimpleSubscriptions++
	case protocol.StatusFull:
		s.interval.TCPFullSubscriptions++
	default:
		return
	}
	s.interval.TCPSubscriptions++
}

func (s *Stats) RecordSlowClientReplacement() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval.SlowClientReplacements++
}

func (s *Stats) SnapshotReset() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.interval
	out.LastValidNMEA = s.lastValidNMEA
	s.interval = newSnapshot()
	return out
}
