package aggregate

import (
	"math"
	"sort"

	"gnssagent/internal/nmea"
)

const (
	constellationGPS     = "GPS"
	constellationBeiDou  = "BeiDou"
	constellationGLONASS = "GLONASS"
	constellationGalileo = "Galileo"
)

type SatelliteKey struct {
	Constellation string
	PRN           string
}

type GSAIdentity struct {
	Talker   string
	SystemID nmea.Field[uint8]
	PRNs     []string
}

type CountResult struct {
	Value uint8
	Valid bool
}

type gsvSatellite struct {
	cn0 nmea.Field[float32]
}

type completeGSV struct {
	visible    uint8
	satellites map[string]gsvSatellite
}

type gsvStreamKey struct {
	talker   string
	signalID nmea.Field[uint8]
}

type gsvGeneration struct {
	total   int
	visible uint8
	packets map[int]*nmea.GSV
}

type gsvStream struct {
	generations []*gsvGeneration
	conflict    bool
}

func constellationForSystemID(systemID uint8) string {
	switch systemID {
	case 1:
		return constellationGPS
	case 2:
		return constellationGLONASS
	case 3:
		return constellationGalileo
	case 4:
		return constellationBeiDou
	default:
		return ""
	}
}

func constellationForTalker(talker string) string {
	switch talker {
	case "GP":
		return constellationGPS
	case "BD", "GB":
		return constellationBeiDou
	case "GL":
		return constellationGLONASS
	case "GA":
		return constellationGalileo
	default:
		return ""
	}
}

func collectCompleteGSVs(sentences []nmea.Sentence) map[string]completeGSV {
	streams := make(map[gsvStreamKey]*gsvStream)
	for _, sentence := range sentences {
		if sentence.Kind != nmea.KindGSV || sentence.GSV == nil {
			continue
		}
		if constellationForTalker(sentence.Talker) == "" {
			continue
		}
		gsv := sentence.GSV
		if !validGSVHeader(gsv) {
			continue
		}
		key := gsvStreamKey{talker: sentence.Talker, signalID: gsv.SignalID}
		stream := streams[key]
		if stream == nil {
			stream = &gsvStream{}
			streams[key] = stream
		}
		stream.add(gsv)
	}

	complete := make(map[string]completeGSV)
	conflicted := make(map[string]bool)
	keys := make([]gsvStreamKey, 0, len(streams))
	for key := range streams {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].talker != keys[right].talker {
			return keys[left].talker < keys[right].talker
		}
		if keys[left].signalID.Valid != keys[right].signalID.Valid {
			return !keys[left].signalID.Valid
		}
		return keys[left].signalID.Value < keys[right].signalID.Value
	})
	for _, key := range keys {
		constellation := constellationForTalker(key.talker)
		set, valid, streamConflict := streams[key].completeSet()
		if streamConflict {
			delete(complete, constellation)
			conflicted[constellation] = true
			continue
		}
		if !valid || conflicted[constellation] {
			continue
		}
		if existing, exists := complete[constellation]; exists {
			merged, sameMembership := mergeCompleteGSV(existing, set)
			if !sameMembership {
				delete(complete, constellation)
				conflicted[constellation] = true
				continue
			}
			complete[constellation] = merged
			continue
		}
		complete[constellation] = set
	}
	return complete
}

func (stream *gsvStream) add(packet *nmea.GSV) {
	var candidates []*gsvGeneration
	for _, generation := range stream.generations {
		if generation.total != packet.TotalMessages || generation.visible != packet.VisibleCount.Value || generation.complete() {
			continue
		}
		if existing, occupied := generation.packets[packet.MessageNumber]; occupied {
			if equalGSVPacket(existing, packet) {
				return
			}
			continue
		}
		candidates = append(candidates, generation)
	}
	if len(candidates) > 1 {
		stream.conflict = true
		return
	}
	if len(candidates) == 1 {
		candidates[0].packets[packet.MessageNumber] = packet
		return
	}
	stream.generations = append(stream.generations, &gsvGeneration{
		total: packet.TotalMessages, visible: packet.VisibleCount.Value,
		packets: map[int]*nmea.GSV{packet.MessageNumber: packet},
	})
}

func (generation *gsvGeneration) complete() bool {
	return generation.total > 0 && len(generation.packets) == generation.total
}

func (stream *gsvStream) completeSet() (completeGSV, bool, bool) {
	if stream.conflict {
		return completeGSV{}, false, true
	}
	var combined completeGSV
	haveComplete := false
	for _, generation := range stream.generations {
		if !generation.complete() {
			continue
		}
		set, valid := generation.completeSet()
		if !valid {
			return completeGSV{}, false, true
		}
		if !haveComplete {
			combined = set
			haveComplete = true
			continue
		}
		var sameMembership bool
		combined, sameMembership = mergeCompleteGSV(combined, set)
		if !sameMembership {
			return completeGSV{}, false, true
		}
	}
	return combined, haveComplete, false
}

func (generation *gsvGeneration) completeSet() (completeGSV, bool) {
	expectedTotal := (int(generation.visible) + 3) / 4
	if expectedTotal == 0 {
		expectedTotal = 1
	}
	if expectedTotal != generation.total {
		return completeGSV{}, false
	}
	set := completeGSV{visible: generation.visible, satellites: make(map[string]gsvSatellite)}
	for number := 1; number <= generation.total; number++ {
		packet := generation.packets[number]
		if packet == nil || len(packet.Satellites) != expectedPacketSatellites(generation.visible, number) {
			return completeGSV{}, false
		}
		for _, satellite := range packet.Satellites {
			if satellite.PRN == "" {
				return completeGSV{}, false
			}
			if _, duplicate := set.satellites[satellite.PRN]; duplicate {
				return completeGSV{}, false
			}
			cn0 := satellite.CN0
			if cn0.Valid && (math.IsNaN(float64(cn0.Value)) || math.IsInf(float64(cn0.Value), 0) || cn0.Value < 0) {
				cn0 = nmea.Field[float32]{}
			}
			set.satellites[satellite.PRN] = gsvSatellite{cn0: cn0}
		}
	}
	return set, len(set.satellites) == int(set.visible)
}

func mergeCompleteGSV(left, right completeGSV) (completeGSV, bool) {
	if left.visible != right.visible || len(left.satellites) != len(right.satellites) {
		return completeGSV{}, false
	}
	merged := completeGSV{visible: left.visible, satellites: make(map[string]gsvSatellite, len(left.satellites))}
	for prn, satellite := range left.satellites {
		other, exists := right.satellites[prn]
		if !exists {
			return completeGSV{}, false
		}
		if satellite.cn0 != other.cn0 {
			satellite.cn0 = nmea.Field[float32]{}
		}
		merged.satellites[prn] = satellite
	}
	return merged, true
}

func equalGSVPacket(left, right *nmea.GSV) bool {
	if left.TotalMessages != right.TotalMessages || left.MessageNumber != right.MessageNumber ||
		left.VisibleCount != right.VisibleCount || left.SignalID != right.SignalID || len(left.Satellites) != len(right.Satellites) {
		return false
	}
	for index := range left.Satellites {
		if left.Satellites[index] != right.Satellites[index] {
			return false
		}
	}
	return true
}

func validGSVHeader(gsv *nmea.GSV) bool {
	return gsv.TotalMessages > 0 && gsv.MessageNumber > 0 && gsv.MessageNumber <= gsv.TotalMessages && gsv.VisibleCount.Valid
}

func expectedPacketSatellites(visible uint8, messageNumber int) int {
	remaining := int(visible) - (messageNumber-1)*4
	if remaining <= 0 {
		return 0
	}
	if remaining > 4 {
		return 4
	}
	return remaining
}

func gsaIdentities(sentences []nmea.Sentence) []GSAIdentity {
	var identities []GSAIdentity
	for _, sentence := range sentences {
		if sentence.Kind == nmea.KindGSA && sentence.GSA != nil {
			identities = append(identities, GSAIdentity{
				Talker: sentence.Talker, SystemID: sentence.GSA.SystemID, PRNs: sentence.GSA.PRNs,
			})
		}
	}
	return identities
}

func resolveConstellation(identity GSAIdentity, prn string, complete map[string]completeGSV) string {
	if identity.SystemID.Valid {
		return constellationForSystemID(identity.SystemID.Value)
	}
	if constellation := constellationForTalker(identity.Talker); constellation != "" {
		return constellation
	}
	if identity.Talker != "GN" {
		return ""
	}
	match := ""
	for constellation, set := range complete {
		if _, exists := set.satellites[prn]; !exists {
			continue
		}
		if match != "" {
			return ""
		}
		match = constellation
	}
	return match
}

func countUsedSatellites(identities []GSAIdentity, complete map[string]completeGSV) CountResult {
	if len(identities) == 0 {
		return CountResult{}
	}
	resolved := make(map[SatelliteKey]struct{})
	resolvedRaw := make(map[string]struct{})
	unresolved := make(map[string]int)
	for _, identity := range identities {
		for _, prn := range identity.PRNs {
			constellation := resolveConstellation(identity, prn, complete)
			if constellation != "" {
				resolved[SatelliteKey{Constellation: constellation, PRN: prn}] = struct{}{}
				resolvedRaw[prn] = struct{}{}
				continue
			}
			if identity.Talker != "GN" || identity.SystemID.Valid || prn == "" {
				return CountResult{}
			}
			unresolved[prn]++
		}
	}
	count := len(resolved)
	for prn, occurrences := range unresolved {
		if occurrences != 1 {
			return CountResult{}
		}
		if _, collision := resolvedRaw[prn]; collision {
			return CountResult{}
		}
		count++
	}
	if count > math.MaxUint8 {
		return CountResult{}
	}
	return CountResult{Value: uint8(count), Valid: true}
}

func averageUsedCN0(identities []GSAIdentity, complete map[string]completeGSV) (float32, bool) {
	used := make(map[SatelliteKey]struct{})
	for _, identity := range identities {
		for _, prn := range identity.PRNs {
			constellation := resolveConstellation(identity, prn, complete)
			if constellation != "" {
				used[SatelliteKey{Constellation: constellation, PRN: prn}] = struct{}{}
			}
		}
	}
	var sum float64
	count := 0
	for key := range used {
		set, exists := complete[key.Constellation]
		if !exists {
			continue
		}
		satellite, exists := set.satellites[key.PRN]
		if !exists || !satellite.cn0.Valid {
			continue
		}
		sum += float64(satellite.cn0.Value)
		count++
	}
	if count == 0 {
		return 0, false
	}
	return float32(sum / float64(count)), true
}
