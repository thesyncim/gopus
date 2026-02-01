//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tracks sMid through the full decode path.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/silk"
)

// TestTV12SMidTracking tracks sMid values using full Decode() path.
func TestTV12SMidTracking(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 150)
	if err != nil {
		t.Skip("Could not load packets")
	}

	silkDec := silk.NewDecoder()
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Tracking sMid at NB→MB transition (pkt 136→137) ===")

	// Process packets 0-136 using full Decode() path
	for i := 0; i <= 136; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// libopus
		libDec.DecodeFloat(pkt, 1920)

		// gopus SILK - use Decode() to update sMid
		if toc.Mode == gopus.ModeSILK {
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if ok {
				silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
			}
		}
	}

	// Check sMid after last NB packet
	sMidAfter136 := silkDec.GetSMid()
	t.Logf("sMid AFTER pkt 136 (last NB): [%d, %d]", sMidAfter136[0], sMidAfter136[1])
	t.Logf("sMid[1] as float: %.6f", float32(sMidAfter136[1])/32768.0)

	// Now decode packet 137 (first MB) - continuing with same decoder
	pkt := packets[137]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	t.Logf("\n=== Decoding packet 137 (first MB, BW=%d) ===", toc.Bandwidth)

	// Check sMid BEFORE decode
	sMidBefore137 := silkDec.GetSMid()
	t.Logf("sMid BEFORE pkt 137 decode: [%d, %d]", sMidBefore137[0], sMidBefore137[1])

	// Decode using full path
	goOut, _ := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	libOut, libN := libDec.DecodeFloat(pkt, 1920)

	// Check sMid AFTER decode
	sMidAfter137 := silkDec.GetSMid()
	t.Logf("sMid AFTER pkt 137 decode: [%d, %d]", sMidAfter137[0], sMidAfter137[1])

	// Compare outputs
	minLen := len(goOut)
	if libN < minLen {
		minLen = libN
	}

	var sumSqErr, sumSqSig float64
	for j := 0; j < minLen; j++ {
		diff := goOut[j] - libOut[j]
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(libOut[j] * libOut[j])
	}
	snr := 10 * math.Log10(sumSqSig/sumSqErr)

	t.Logf("\nSNR: %.1f dB", snr)
	t.Log("First 10 samples at 48kHz:")
	for i := 0; i < 10 && i < minLen; i++ {
		diff := goOut[i] - libOut[i]
		t.Logf("  [%d] go=%.6f lib=%.6f diff=%.6f", i, goOut[i], libOut[i], diff)
	}

	// Check if first gopus sample is derived from sMid[1]
	// The first resampler input is sMid[1], so first 48kHz output should correlate
	t.Logf("\nAnalysis: sMid[1]/32768 = %.6f, first lib output = %.6f",
		float32(sMidBefore137[1])/32768.0, libOut[0])
	t.Log("If gopus first output is 0 but lib is non-zero, sMid wasn't used correctly")
}
