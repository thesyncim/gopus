// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// TestSILKDivergenceLocation finds where MB/WB diverges.
func TestSILKDivergenceLocation(t *testing.T) {
	tests := []struct {
		name   string
		vector string
	}{
		{"MB", "testvector03"},
		{"WB", "testvector04"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + tt.vector + ".bit"

			packets, err := loadPacketsSimple(bitFile, 5)
			if err != nil {
				t.Skip("Could not load packets")
			}

			goDec, _ := gopus.NewDecoder(48000, 1)
			libDec, _ := NewLibopusDecoder(48000, 1)
			if libDec == nil {
				t.Skip("Could not create libopus decoder")
			}
			defer libDec.Destroy()

			pkt := packets[0]
			goOut, _ := goDec.DecodeFloat32(pkt)
			libOut, libN := libDec.DecodeFloat(pkt, 5760)

			minLen := len(goOut)
			if libN < minLen {
				minLen = libN
			}

			// Find all divergent samples
			t.Log("Divergent samples:")
			count := 0
			for i := 0; i < minLen; i++ {
				diff := goOut[i] - libOut[i]
				if diff != 0 && (diff < -1e-7 || diff > 1e-7) {
					if count < 20 { // Show first 20
						t.Logf("  [%d] go=%.6f lib=%.6f diff=%.9f",
							i, goOut[i], libOut[i], diff)
					}
					count++
				}
			}
			t.Logf("Total divergent samples: %d / %d (%.1f%%)",
				count, minLen, 100.0*float64(count)/float64(minLen))
		})
	}
}
