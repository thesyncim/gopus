//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests fresh decode of packet 826 with no prior state.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// TestTV12Packet826FreshDecode compares fresh (no prior state) decode of packet 826.
func TestTV12Packet826FreshDecode(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create FRESH libopus 48kHz decoder
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Create FRESH gopus SILK decoder
	silkDec := silk.NewDecoder()

	// Get packet 826
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	t.Logf("Packet 826: Mode=%v, BW=%v (NB 8kHz)", toc.Mode, toc.Bandwidth)

	// Decode with FRESH gopus (no prior packets)
	goOut, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("gopus error: %v", err)
	}

	// Decode with FRESH libopus (no prior packets)
	libOut, libN := libDec.DecodeFloat(pkt, 1920)

	t.Logf("Gopus 48kHz output: %d samples", len(goOut))
	t.Logf("Libopus 48kHz output: %d samples", libN)

	// Compare first 30 samples
	t.Log("\n=== FRESH DECODE comparison (no prior state) ===")
	t.Log("First 30 samples at 48kHz:")
	for i := 0; i < 30 && i < len(goOut) && i < libN; i++ {
		t.Logf("  [%2d] go=%+.6f lib=%+.6f diff=%+.6f",
			i, goOut[i], libOut[i], goOut[i]-libOut[i])
	}

	// Check if gopus output matches libopus for fresh decode
	match := true
	for i := 0; i < len(goOut) && i < libN; i++ {
		diff := goOut[i] - libOut[i]
		if diff > 0.0001 || diff < -0.0001 {
			match = false
			break
		}
	}

	if match {
		t.Log("\n>>> FRESH decode matches! Bug is in state accumulation, not algorithm.")
	} else {
		t.Log("\n>>> FRESH decode differs! Bug is in core algorithm.")
	}
}

// TestTV12Packet826CompareNativeSamples compares native SILK samples from fresh decode.
func TestTV12Packet826CompareNativeSamples(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create FRESH gopus SILK decoder
	silkDec := silk.NewDecoder()

	// Get packet 826
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	// Decode at native rate
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}

	t.Logf("Native SILK samples (8kHz): %d", len(nativeSamples))
	t.Log("First 30 native samples:")
	for i := 0; i < 30 && i < len(nativeSamples); i++ {
		int16Val := int16(nativeSamples[i] * 32768.0)
		t.Logf("  [%2d] float=%.6f int16=%d", i, nativeSamples[i], int16Val)
	}

	// Calculate RMS of native samples
	var sumSq float64
	for _, v := range nativeSamples {
		sumSq += float64(v * v)
	}
	rms := float32(sumSq / float64(len(nativeSamples)))
	t.Logf("\nRMS of native samples: %.6f", rms)

	// What we expect: libopus fresh decode of packet 826 at 48kHz gives ~0.001 for first samples
	// For 6x upsampling (8k->48k), this would come from native samples of roughly similar magnitude
	// But we're getting native samples of ~0.0003, which after resampling give ~0.00003
	// This suggests either the SILK decode is wrong, or something else is amiss

	t.Log("\n=== Analysis ===")
	if rms < 0.001 {
		t.Log("Native samples are very small (RMS < 0.001)")
		t.Log("This could be correct if the audio content is very quiet,")
		t.Log("or could indicate a decode bug.")
	}
}
