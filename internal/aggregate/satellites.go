package aggregate

import (
	"math"

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

type gsvAssembly struct {
	total   int
	visible uint8
	packets map[int]*nmea.GSV
	invalid bool
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
	assemblies := make(map[string]*gsvAssembly)
	for _, sentence := range sentences {
		if sentence.Kind != nmea.KindGSV || sentence.GSV == nil {
			continue
		}
		if constellationForTalker(sentence.Talker) == "" {
			continue
		}
		gsv := sentence.GSV
		assembly := assemblies[sentence.Talker]
		if assembly == nil {
			assembly = &gsvAssembly{total: gsv.TotalMessages, packets: make(map[int]*nmea.GSV)}
			if gsv.VisibleCount.Valid {
				assembly.visible = gsv.VisibleCount.Value
			}
			assemblies[sentence.Talker] = assembly
		}
		if !validGSVHeader(gsv) || gsv.TotalMessages != assembly.total || gsv.VisibleCount.Value != assembly.visible {
			assembly.invalid = true
			continue
		}
		if _, duplicate := assembly.packets[gsv.MessageNumber]; duplicate {
			assembly.invalid = true
			continue
		}
		assembly.packets[gsv.MessageNumber] = gsv
	}

	complete := make(map[string]completeGSV)
	conflicted := make(map[string]bool)
	for _, talker := range []string{"GP", "BD", "GB", "GL", "GA"} {
		assembly := assemblies[talker]
		if assembly == nil {
			continue
		}
		if assembly.invalid || assembly.total <= 0 || len(assembly.packets) != assembly.total {
			continue
		}
		expectedTotal := (int(assembly.visible) + 3) / 4
		if expectedTotal == 0 {
			expectedTotal = 1
		}
		if expectedTotal != assembly.total {
			continue
		}
		set := completeGSV{visible: assembly.visible, satellites: make(map[string]gsvSatellite)}
		valid := true
		for number := 1; number <= assembly.total; number++ {
			packet := assembly.packets[number]
			if packet == nil || len(packet.Satellites) != expectedPacketSatellites(assembly.visible, number) {
				valid = false
				break
			}
			for _, satellite := range packet.Satellites {
				if satellite.PRN == "" {
					valid = false
					break
				}
				if _, duplicate := set.satellites[satellite.PRN]; duplicate {
					valid = false
					break
				}
				cn0 := satellite.CN0
				if cn0.Valid && (math.IsNaN(float64(cn0.Value)) || math.IsInf(float64(cn0.Value), 0) || cn0.Value < 0) {
					cn0 = nmea.Field[float32]{}
				}
				set.satellites[satellite.PRN] = gsvSatellite{cn0: cn0}
			}
			if !valid {
				break
			}
		}
		if valid && len(set.satellites) == int(set.visible) {
			constellation := constellationForTalker(talker)
			if conflicted[constellation] {
				continue
			}
			if existing, exists := complete[constellation]; exists && !sameCompleteGSV(existing, set) {
				delete(complete, constellation)
				conflicted[constellation] = true
				continue
			}
			complete[constellation] = set
		}
	}
	return complete
}

func sameCompleteGSV(left, right completeGSV) bool {
	if left.visible != right.visible || len(left.satellites) != len(right.satellites) {
		return false
	}
	for prn, satellite := range left.satellites {
		other, exists := right.satellites[prn]
		if !exists || satellite.cn0 != other.cn0 {
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
