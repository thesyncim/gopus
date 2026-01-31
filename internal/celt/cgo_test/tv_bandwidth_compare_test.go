// Package cgo compares bandwidth patterns across test vectors.
package cgo

import (
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTVBandwidthPatterns shows bandwidth transitions in different test vectors.
func TestTVBandwidthPatterns(t *testing.T) {
	testVectors := []struct {
		name string
		file string
	}{
		{"TV02 (SILK mono, PASS)", "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"},
		{"TV03 (SILK mono, PASS)", "../../../internal/testvectors/testdata/opus_testvectors/testvector03.bit"},
		{"TV04 (SILK mono, PASS)", "../../../internal/testvectors/testdata/opus_testvectors/testvector04.bit"},
		{"TV12 (Complex, FAIL)", "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"},
	}

	for _, tv := range testVectors {
		t.Run(tv.name, func(t *testing.T) {
			_, err := os.Stat(tv.file)
			if os.IsNotExist(err) {
				t.Skip("File not found")
			}

			packets, err := loadPacketsSimple(tv.file, 200)
			if err != nil {
				t.Skip("Could not load packets")
			}

			// Count mode and bandwidth patterns
			modeCount := make(map[gopus.Mode]int)
			bwCount := make(map[gopus.Bandwidth]int)
			prevBW := gopus.Bandwidth(255)
			bwTransitions := []int{}

			for i, pkt := range packets {
				toc := gopus.ParseTOC(pkt[0])
				modeCount[toc.Mode]++
				bwCount[toc.Bandwidth]++

				if toc.Bandwidth != prevBW && prevBW != gopus.Bandwidth(255) {
					bwTransitions = append(bwTransitions, i)
				}
				prevBW = toc.Bandwidth
			}

			t.Logf("%s (%d packets):", tv.name, len(packets))
			t.Logf("  Modes: SILK=%d, Hybrid=%d, CELT=%d",
				modeCount[gopus.ModeSILK], modeCount[gopus.ModeHybrid], modeCount[gopus.ModeCELT])
			t.Logf("  Bandwidths: NB=%d, MB=%d, WB=%d, SWB=%d, FB=%d",
				bwCount[gopus.BandwidthNarrowband], bwCount[gopus.BandwidthMediumband],
				bwCount[gopus.BandwidthWideband], bwCount[gopus.BandwidthSuperwideband],
				bwCount[gopus.BandwidthFullband])
			t.Logf("  BW transitions: %d at packets %v", len(bwTransitions), bwTransitions)
		})
	}
}
