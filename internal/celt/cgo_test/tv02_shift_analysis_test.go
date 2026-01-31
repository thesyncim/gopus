// Package cgo tests TV02 SILK decoder shift analysis.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV02ShiftAnalysis analyzes potential sample shift between Go and libopus
func TestTV02ShiftAnalysis(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 100)
	if err != nil {
		t.Skip("Could not load packets")
	}

	silkDec := silk.NewDecoder()
	libDec8k, _ := NewLibopusDecoder(8000, 1)
	if libDec8k == nil {
		t.Skip("Could not create 8k libopus decoder")
	}
	defer libDec8k.Destroy()

	// Test packet 58 specifically (mentioned in issue)
	pkt := packets[58]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}
	libNative, libN := libDec8k.DecodeFloat(pkt, 320)
	if libN <= 0 {
		t.Fatal("libopus decode failed")
	}

	t.Logf("=== Checking sample shift in packet 58 ===")
	t.Logf("Go samples: %d, Lib samples: %d", len(goNative), libN)

	// Look for correlation with different offsets
	t.Log("\n=== Testing different offsets (go[i+offset] vs lib[i]) ===")
	bestOffset := 0
	bestSNR := -1000.0
	for offset := -10; offset <= 10; offset++ {
		var sumSqErr, sumSqSig float64
		for j := 0; j < libN-20; j++ {
			goIdx := j + offset
			if goIdx < 0 || goIdx >= len(goNative) {
				continue
			}
			diff := goNative[goIdx] - libNative[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libNative[j] * libNative[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("Offset %3d: SNR=%6.1f dB", offset, snr)
		if snr > bestSNR {
			bestSNR = snr
			bestOffset = offset
		}
	}
	t.Logf("\nBest offset: %d with SNR=%.1f dB", bestOffset, bestSNR)

	// Print detailed comparison at best offset
	t.Log("\n=== Sample comparison at best offset ===")
	t.Logf("%4s %12s %12s %12s", "idx", "go[i+off]", "lib[i]", "diff")
	for j := 0; j < 20 && j+bestOffset < len(goNative) && j < libN; j++ {
		goIdx := j + bestOffset
		if goIdx < 0 {
			continue
		}
		diff := goNative[goIdx] - libNative[j]
		t.Logf("%4d %+12.6f %+12.6f %+12.6f", j, goNative[goIdx], libNative[j], diff)
	}

	// Print raw comparison (no offset)
	t.Log("\n=== Raw sample comparison (first 20 samples, no offset) ===")
	t.Logf("%4s %12s %12s", "idx", "go", "lib")
	for j := 0; j < 20 && j < len(goNative) && j < libN; j++ {
		t.Logf("%4d %+12.6f %+12.6f", j, goNative[j], libNative[j])
	}
}
