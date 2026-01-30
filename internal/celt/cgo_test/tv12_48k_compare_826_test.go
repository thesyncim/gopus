// Package cgo compares gopus and libopus 48kHz output at packet 826.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12_48kCompare826 compares gopus vs libopus 48kHz output at packet 826.
func TestTV12_48kCompare826(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus decoder (full Opus path, 48kHz output)
	goDec, _ := gopus.NewDecoder(48000, 1)

	// Create libopus decoder (48kHz output)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Processing packets 0-825 ===")
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		goDec.DecodeFloat32(pkt)
		libDec.DecodeFloat(pkt, 1920)
	}

	// Decode packet 826
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 826: Mode=%v BW=%d", toc.Mode, toc.Bandwidth)

	goOut, _ := goDec.DecodeFloat32(pkt)
	libOut, libN := libDec.DecodeFloat(pkt, 1920)

	goMax := maxAbs(goOut)
	libMax := maxAbsSlice(libOut[:libN])

	t.Logf("\n=== 48kHz Output comparison ===")
	t.Logf("Gopus 48kHz: %d samples, max=%.6f", len(goOut), goMax)
	t.Logf("Libopus 48kHz: %d samples, max=%.6f", libN, libMax)

	// Calculate SNR
	minLen := len(goOut)
	if libN < minLen {
		minLen = libN
	}
	var sumSqErr, sumSqSig float64
	for i := 0; i < minLen; i++ {
		diff := goOut[i] - libOut[i]
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(libOut[i] * libOut[i])
	}
	snr := 10 * math.Log10(sumSqSig/sumSqErr)
	if math.IsNaN(snr) || math.IsInf(snr, 1) {
		snr = 999.0
	}
	t.Logf("SNR: %.1f dB", snr)

	// Show first 20 samples comparison
	t.Log("\nFirst 20 samples comparison:")
	for i := 0; i < 20 && i < minLen; i++ {
		diff := goOut[i] - libOut[i]
		t.Logf("  [%2d] go=%.6f lib=%.6f diff=%.6f", i, goOut[i], libOut[i], diff)
	}

	// Show samples around index 480 (where we saw non-zero output)
	t.Log("\nSamples 480-500 comparison:")
	for i := 480; i < 500 && i < minLen; i++ {
		diff := goOut[i] - libOut[i]
		t.Logf("  [%3d] go=%.6f lib=%.6f diff=%.6f", i, goOut[i], libOut[i], diff)
	}
}
