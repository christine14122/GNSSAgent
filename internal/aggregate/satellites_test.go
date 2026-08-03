package aggregate

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gnssagent/internal/model"
	"gnssagent/internal/nmea"
)

func TestGSAIdentityResolutionPriority(t *testing.T) {
	base := time.Unix(10_000, 0)
	tests := []struct {
		name      string
		gsa       nmea.Sentence
		gsv       nmea.Sentence
		wantCount uint8
		wantCN0   float32
	}{
		{
			name: "system ID has priority over talker",
			gsa: nmea.Sentence{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{
				SystemID: field(uint8(4)), PRNs: []string{"01"},
			}},
			gsv: gsvSentence(base, "BD", 1, 1, 1, satellite("01", 41)), wantCount: 1, wantCN0: 41,
		},
		{
			name: "specific talker supplies identity",
			gsa:  nmea.Sentence{Kind: nmea.KindGSA, Talker: "GL", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"65"}}},
			gsv:  gsvSentence(base, "GL", 1, 1, 1, satellite("65", 37)), wantCount: 1, wantCN0: 37,
		},
		{
			name: "GN resolves by unique complete GSV correlation",
			gsa:  nmea.Sentence{Kind: nmea.KindGSA, Talker: "GN", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"201"}}},
			gsv:  gsvSentence(base, "BD", 1, 1, 1, satellite("201", 44)), wantCount: 1, wantCN0: 44,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateSentences(t, []nmea.Sentence{tt.gsa, tt.gsv})
			if got.FieldValidityMask&model.FullUsedSatellitesValid == 0 || got.UsedSatellites != tt.wantCount {
				t.Fatalf("used=%d mask=%#x", got.UsedSatellites, got.FieldValidityMask)
			}
			if got.FieldValidityMask&model.FullAvgUsedCN0Valid == 0 || got.AvgUsedCN0 != tt.wantCN0 {
				t.Fatalf("CN0=%v mask=%#x", got.AvgUsedCN0, got.FieldValidityMask)
			}
		})
	}
}

func TestUnresolvedGNDoesNotUsePRNRangesOrSentenceOrder(t *testing.T) {
	base := time.Unix(11_000, 0)
	got := aggregateSentences(t, []nmea.Sentence{
		{Kind: nmea.KindGSA, Talker: "GN", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"201", "01"}}},
	})
	if got.FieldValidityMask&model.FullUsedSatellitesValid == 0 || got.UsedSatellites != 2 {
		t.Fatalf("unambiguous unresolved raw PRNs should count: %+v", got)
	}
	if got.FieldValidityMask&model.FullAvgUsedCN0Valid != 0 || got.AvgUsedCN0 != 0 {
		t.Fatalf("unresolved PRN was guessed for CN0: %+v", got)
	}
}

func TestAmbiguousUnresolvedUsedCountsAreInvalid(t *testing.T) {
	base := time.Unix(12_000, 0)
	tests := []struct {
		name      string
		sentences []nmea.Sentence
	}{
		{
			name: "repeated unresolved PRN across GN GSA",
			sentences: []nmea.Sentence{
				{Kind: nmea.KindGSA, Talker: "GN", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
				{Kind: nmea.KindGSA, Talker: "GN", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
			},
		},
		{
			name: "unknown raw PRN collides with known identity",
			sentences: []nmea.Sentence{
				{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
				{Kind: nmea.KindGSA, Talker: "GN", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateSentences(t, tt.sentences)
			if got.FieldValidityMask&model.FullUsedSatellitesValid != 0 || got.UsedSatellites != 0 {
				t.Fatalf("ambiguous count published: %+v", got)
			}
		})
	}
}

func TestUniqueGSVCorrelationResolvesCollisionAndDeduplicates(t *testing.T) {
	base := time.Unix(13_000, 0)
	got := aggregateSentences(t, []nmea.Sentence{
		{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
		{Kind: nmea.KindGSA, Talker: "GN", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
		gsvSentence(base, "GP", 1, 1, 1, satellite("01", 40)),
	})
	if got.FieldValidityMask&model.FullUsedSatellitesValid == 0 || got.UsedSatellites != 1 {
		t.Fatalf("resolved collision did not deduplicate: %+v", got)
	}
	if got.FieldValidityMask&model.FullAvgUsedCN0Valid == 0 || got.AvgUsedCN0 != 40 {
		t.Fatalf("resolved collision CN0: %+v", got)
	}
}

func TestGGAUsedCountTakesPrecedenceIncludingZero(t *testing.T) {
	base := time.Unix(14_000, 0)
	got := aggregateSentences(t, []nmea.Sentence{
		{Kind: nmea.KindGGA, ReceivedAt: base, GGA: &nmea.GGA{UsedSatellites: field(uint8(0))}},
		{Kind: nmea.KindGSA, Talker: "GN", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
		{Kind: nmea.KindGSA, Talker: "GN", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
	})
	if got.FieldValidityMask&model.FullUsedSatellitesValid == 0 || got.UsedSatellites != 0 {
		t.Fatalf("GGA zero did not bypass ambiguous fallback: %+v", got)
	}
}

func TestUsedCountIsInvalidWithoutGGAOrGSA(t *testing.T) {
	base := time.Unix(14_500, 0)
	got := aggregateSentences(t, []nmea.Sentence{gsvSentence(base, "GP", 1, 1, 1, satellite("01", 40))})
	if got.FieldValidityMask&model.FullUsedSatellitesValid != 0 || got.UsedSatellites != 0 {
		t.Fatalf("GSV alone invented a used count: %+v", got)
	}

	got = aggregateSentences(t, []nmea.Sentence{{Kind: nmea.KindGSA, Talker: "GN", ReceivedAt: base, GSA: &nmea.GSA{}}})
	if got.FieldValidityMask&model.FullUsedSatellitesValid == 0 || got.UsedSatellites != 0 {
		t.Fatalf("present empty GSA did not publish a valid zero: %+v", got)
	}
}

func TestResolvedUsedIdentityDeduplication(t *testing.T) {
	base := time.Unix(15_000, 0)
	tests := []struct {
		name string
		gsa  []nmea.Sentence
		want uint8
	}{
		{
			name: "same identity deduplicates",
			gsa: []nmea.Sentence{
				{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
				{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
			}, want: 1,
		},
		{
			name: "same numeric PRN in different constellations is distinct",
			gsa: []nmea.Sentence{
				{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
				{Kind: nmea.KindGSA, Talker: "BD", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
			}, want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateSentences(t, tt.gsa)
			if got.FieldValidityMask&model.FullUsedSatellitesValid == 0 || got.UsedSatellites != tt.want {
				t.Fatalf("used=%d mask=%#x", got.UsedSatellites, got.FieldValidityMask)
			}
		})
	}
}

func TestGSVCompletenessAndCounts(t *testing.T) {
	base := time.Unix(16_000, 0)
	tests := []struct {
		name      string
		sentences []nmea.Sentence
		bit       uint64
		want      uint8
		valid     bool
	}{
		{
			name: "complete in order", bit: model.FullGPSSatellitesValid, want: 5, valid: true,
			sentences: []nmea.Sentence{
				gsvSentence(base, "GP", 2, 1, 5, satellite("01", 1), satellite("02", 2), satellite("03", 3), satellite("04", 4)),
				gsvSentence(base, "GP", 2, 2, 5, satellite("05", 5)),
			},
		},
		{
			name: "complete out of order", bit: model.FullBeiDouSatellitesValid, want: 5, valid: true,
			sentences: []nmea.Sentence{
				gsvSentence(base, "BD", 2, 2, 5, satellite("05", 5)),
				gsvSentence(base, "BD", 2, 1, 5, satellite("01", 1), satellite("02", 2), satellite("03", 3), satellite("04", 4)),
			},
		},
		{name: "incomplete", bit: model.FullGPSSatellitesValid, sentences: []nmea.Sentence{gsvSentence(base, "GP", 2, 1, 5, satellite("01", 1), satellite("02", 2), satellite("03", 3), satellite("04", 4))}},
		{
			name: "inconsistent duplicate", bit: model.FullGPSSatellitesValid,
			sentences: []nmea.Sentence{
				gsvSentence(base, "GP", 2, 1, 5, satellite("01", 1), satellite("02", 2), satellite("03", 3), satellite("04", 4)),
				gsvSentence(base, "GP", 2, 1, 5, satellite("11", 1), satellite("12", 2), satellite("13", 3), satellite("14", 4)),
				gsvSentence(base, "GP", 2, 2, 5, satellite("05", 5)),
			},
		},
		{name: "zero visible is valid", bit: model.FullGLONASSSatellitesValid, valid: true, sentences: []nmea.Sentence{gsvSentence(base, "GL", 1, 1, 0)}},
		{name: "GN is never guessed", sentences: []nmea.Sentence{gsvSentence(base, "GN", 1, 1, 1, satellite("01", 40))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateSentences(t, tt.sentences)
			if tt.bit != 0 && (got.FieldValidityMask&tt.bit != 0) != tt.valid {
				t.Fatalf("mask=%#x bit=%#x", got.FieldValidityMask, tt.bit)
			}
			if tt.valid {
				var value uint8
				switch tt.bit {
				case model.FullGPSSatellitesValid:
					value = got.GPSSatellites
				case model.FullBeiDouSatellitesValid:
					value = got.BeiDouSatellites
				case model.FullGLONASSSatellitesValid:
					value = got.GLONASSSatellites
				}
				if value != tt.want {
					t.Fatalf("count=%d want=%d", value, tt.want)
				}
			}
			if tt.name == "GN is never guessed" {
				bits := model.FullGPSSatellitesValid | model.FullBeiDouSatellitesValid | model.FullGLONASSSatellitesValid | model.FullGalileoSatellitesValid
				if got.FieldValidityMask&bits != 0 {
					t.Fatalf("GN GSV set a constellation bit: %+v", got)
				}
			}
		})
	}
}

func TestAllFourConstellationCountBits(t *testing.T) {
	base := time.Unix(17_000, 0)
	got := aggregateSentences(t, []nmea.Sentence{
		gsvSentence(base, "GP", 1, 1, 1, satellite("01", 1)),
		gsvSentence(base, "GB", 1, 1, 1, satellite("02", 2)),
		gsvSentence(base, "GL", 1, 1, 1, satellite("03", 3)),
		gsvSentence(base, "GA", 1, 1, 1, satellite("04", 4)),
	})
	bits := model.FullGPSSatellitesValid | model.FullBeiDouSatellitesValid | model.FullGLONASSSatellitesValid | model.FullGalileoSatellitesValid
	if got.FieldValidityMask&bits != bits || got.GPSSatellites != 1 || got.BeiDouSatellites != 1 || got.GLONASSSatellites != 1 || got.GalileoSatellites != 1 {
		t.Fatalf("four counts: %+v", got)
	}
}

func TestBeiDouTalkerAliasesAssembleIndependently(t *testing.T) {
	base := time.Unix(17_500, 0)
	got := aggregateSentences(t, []nmea.Sentence{
		gsvSentence(base, "BD", 1, 1, 1, satellite("01", 40)),
		gsvSentence(base, "GB", 2, 1, 5, satellite("11", 20), satellite("12", 21), satellite("13", 22), satellite("14", 23)),
	})
	if got.FieldValidityMask&model.FullBeiDouSatellitesValid == 0 || got.BeiDouSatellites != 1 {
		t.Fatalf("incomplete GB poisoned complete BD: %+v", got)
	}
}

func TestAverageUsedCN0Rules(t *testing.T) {
	base := time.Unix(18_000, 0)
	tests := []struct {
		name      string
		sentences []nmea.Sentence
		valid     bool
		want      float32
	}{
		{
			name: "mean and identity dedup", valid: true, want: 30,
			sentences: []nmea.Sentence{
				{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01", "01", "02"}}},
				gsvSentence(base, "GP", 1, 1, 2, satellite("01", 20), satellite("02", 40)),
			},
		},
		{
			name: "missing CN0 does not participate", valid: true, want: 20,
			sentences: []nmea.Sentence{
				{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01", "02"}}},
				gsvSentence(base, "GP", 1, 1, 2, satellite("01", 20), nmea.Satellite{PRN: "02"}),
			},
		},
		{
			name: "no matches is invalid",
			sentences: []nmea.Sentence{
				{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
				gsvSentence(base, "GP", 1, 1, 1, satellite("02", 40)),
			},
		},
		{
			name: "ambiguous GN identity does not participate",
			sentences: []nmea.Sentence{
				{Kind: nmea.KindGSA, Talker: "GN", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
				gsvSentence(base, "GP", 1, 1, 1, satellite("01", 20)),
				gsvSentence(base, "BD", 1, 1, 1, satellite("01", 40)),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateSentences(t, tt.sentences)
			if (got.FieldValidityMask&model.FullAvgUsedCN0Valid != 0) != tt.valid || got.AvgUsedCN0 != tt.want {
				t.Fatalf("CN0=%v mask=%#x", got.AvgUsedCN0, got.FieldValidityMask)
			}
		})
	}
}

func TestCombinedFixtureUsesUniqueGSVCorrelation(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "nmea", "combined-fix.nmea")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	base := time.Unix(19_000, 0)
	var sentences []nmea.Sentence
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		sentence, err := nmea.Parse(scanner.Bytes(), base)
		if err != nil {
			t.Fatal(err)
		}
		sentences = append(sentences, sentence)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	got := aggregateSentences(t, sentences)
	if got.FieldValidityMask&model.FullUsedSatellitesValid == 0 || got.UsedSatellites != 14 {
		t.Fatalf("fixture used count: %+v", got)
	}
	if got.FieldValidityMask&model.FullAvgUsedCN0Valid == 0 || math.Abs(float64(got.AvgUsedCN0)-530.0/14.0) > 1e-5 {
		t.Fatalf("fixture CN0=%v mask=%#x", got.AvgUsedCN0, got.FieldValidityMask)
	}
}

func TestRequiredSatellitePublicTypes(t *testing.T) {
	_ = SatelliteKey{Constellation: "GPS", PRN: "01"}
	_ = GSAIdentity{Talker: "GN", SystemID: field(uint8(1)), PRNs: []string{"01"}}
	_ = CountResult{Value: 0, Valid: true}
}

func TestReviewSystemIDExactMappings(t *testing.T) {
	base := time.Unix(20_000, 0)
	tests := []struct {
		name        string
		systemID    uint8
		talker      string
		decoyTalker string
	}{
		{name: "1 GPS", systemID: 1, talker: "GP", decoyTalker: "GL"},
		{name: "2 GLONASS", systemID: 2, talker: "GL", decoyTalker: "GP"},
		{name: "3 Galileo", systemID: 3, talker: "GA", decoyTalker: "GP"},
		{name: "4 BeiDou", systemID: 4, talker: "BD", decoyTalker: "GP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateSentences(t, []nmea.Sentence{
				{Kind: nmea.KindGSA, Talker: "GN", ReceivedAt: base, GSA: &nmea.GSA{SystemID: field(tt.systemID), PRNs: []string{"01"}}},
				gsvSentence(base, tt.talker, 1, 1, 1, satellite("01", float32(30+tt.systemID))),
				gsvSentence(base, tt.decoyTalker, 1, 1, 1, satellite("01", 99)),
			})
			if got.FieldValidityMask&model.FullUsedSatellitesValid == 0 || got.UsedSatellites != 1 ||
				got.FieldValidityMask&model.FullAvgUsedCN0Valid == 0 || got.AvgUsedCN0 != float32(30+tt.systemID) {
				t.Fatalf("system ID %d did not map to %s: %+v", tt.systemID, tt.talker, got)
			}
		})
	}
}

func TestReviewUnsupportedSystemIDDoesNotFallBackToTalker(t *testing.T) {
	base := time.Unix(20_100, 0)
	got := aggregateSentences(t, []nmea.Sentence{
		{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{SystemID: field(uint8(5)), PRNs: []string{"01"}}},
		gsvSentence(base, "GP", 1, 1, 1, satellite("01", 40)),
	})
	if got.FieldValidityMask&model.FullUsedSatellitesValid != 0 || got.UsedSatellites != 0 ||
		got.FieldValidityMask&model.FullAvgUsedCN0Valid != 0 || got.AvgUsedCN0 != 0 {
		t.Fatalf("unsupported system ID silently fell back to GP talker: %+v", got)
	}
}

func TestReviewInconsistentGSVHeadersNeverComplete(t *testing.T) {
	base := time.Unix(20_200, 0)
	packetOne := gsvSentence(base, "GP", 2, 1, 5,
		satellite("01", 40), satellite("02", 41), satellite("03", 42), satellite("04", 43))
	tests := []struct {
		name      string
		packetTwo nmea.Sentence
	}{
		{
			name:      "total messages disagree",
			packetTwo: gsvSentence(base, "GP", 3, 2, 5, satellite("05", 44)),
		},
		{
			name:      "visible count disagrees",
			packetTwo: gsvSentence(base, "GP", 2, 2, 6, satellite("05", 44)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateSentences(t, []nmea.Sentence{
				{Kind: nmea.KindGSA, Talker: "GP", ReceivedAt: base, GSA: &nmea.GSA{PRNs: []string{"01"}}},
				packetOne,
				tt.packetTwo,
			})
			if got.FieldValidityMask&model.FullGPSSatellitesValid != 0 || got.GPSSatellites != 0 ||
				got.FieldValidityMask&model.FullAvgUsedCN0Valid != 0 || got.AvgUsedCN0 != 0 {
				t.Fatalf("inconsistent GSV headers completed a set: %+v", got)
			}
		})
	}
}

func TestReviewGSAFallbackOverflowIsInvalid(t *testing.T) {
	prns := make([]string, 256)
	for index := range prns {
		prns[index] = strconv.Itoa(index)
	}
	got := countUsedSatellites([]GSAIdentity{{Talker: "GP", PRNs: prns}}, nil)
	if got.Valid || got.Value != 0 {
		t.Fatalf("256 identities wrapped/truncated: %+v", got)
	}
}
