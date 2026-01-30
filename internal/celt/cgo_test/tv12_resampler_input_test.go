// Package cgo compares resampler input at packet 826.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ResamplerInput826 compares resampler input values.
func TestTV12ResamplerInput826(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	silkDec := silk.NewDecoder()

	// Process packets 0-825
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if ok {
			silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		}
	}

	// Get sMid before packet 826
	sMidBefore := silkDec.GetSMid()
	t.Logf("sMid BEFORE pkt 826: [%d, %d]", sMidBefore[0], sMidBefore[1])
	t.Logf("sMid[1] as float: %.6f", float32(sMidBefore[1])/32768.0)

	// Decode packet 826 at native rate (no resampling)
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	nativeSamples, _ := silkDec.DecodeFrame(&rd, silkBW, duration, true)

	t.Logf("\nNative decode: %d samples at NB (8kHz)", len(nativeSamples))
	t.Log("First 10 native samples:")
	for i := 0; i < 10 && i < len(nativeSamples); i++ {
		t.Logf("  [%d] %.6f", i, nativeSamples[i])
	}

	// Build resampler input the way gopus does
	resamplerInput := make([]float32, len(nativeSamples))
	resamplerInput[0] = float32(sMidBefore[1]) / 32768.0
	copy(resamplerInput[1:], nativeSamples[:len(nativeSamples)-1])

	t.Log("\nResampler input (sMid[1] + native[0:N-2]):")
	t.Log("First 10 resampler inputs:")
	for i := 0; i < 10 && i < len(resamplerInput); i++ {
		t.Logf("  [%d] %.6f", i, resamplerInput[i])
	}

	// In libopus, resampler gets: sMid[1], decoded[0], ..., decoded[N-2]
	// That's exactly what we have in resamplerInput

	t.Log("\n=== Key insight ===")
	t.Logf("First resampler input: %.6f (from sMid[1]=%d)", resamplerInput[0], sMidBefore[1])
	t.Logf("Second resampler input: %.6f (from native[0])", resamplerInput[1])

	// If libopus has larger values, it means either:
	// 1. sMid[1] is larger in libopus
	// 2. Native decode is larger in libopus
	// We established native decode matches (samples 480-500 are perfect)
	// So sMid must be different!

	t.Log("\nExpected libopus first output: ~0.001040")
	t.Log("If sMid[1] were ~34 (0.001038), first output would match")
}
