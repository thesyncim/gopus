// Package cgo compares native decoded samples before resampling for packet 826.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Packet826NativeRateCompare compares native-rate decoded samples.
func TestTV12Packet826NativeRateCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus SILK decoder
	silkDec := silk.NewDecoder()

	// Create libopus decoder at NATIVE rate (8kHz for NB packets)
	// Process all packets to build state
	libDec8k, _ := NewLibopusDecoder(8000, 1)
	if libDec8k == nil {
		t.Skip("Could not create 8kHz libopus decoder")
	}
	defer libDec8k.Destroy()

	libDec12k, _ := NewLibopusDecoder(12000, 1)
	if libDec12k == nil {
		t.Skip("Could not create 12kHz libopus decoder")
	}
	defer libDec12k.Destroy()

	// Process packets 0-825
	t.Log("Processing packets 0-825...")
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		// Gopus: full decode
		silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)

		// Libopus: decode at appropriate native rate
		if silkBW == silk.BandwidthNarrowband {
			libDec8k.DecodeFloat(pkt, 320)
		} else {
			libDec12k.DecodeFloat(pkt, 480)
		}
	}

	// Now compare packet 826 (NB)
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	t.Logf("\n=== Packet 826 (NB) ===")

	// Get sMid before gopus decode
	sMid := silkDec.GetSMid()
	t.Logf("sMid before decode: [%d, %d]", sMid[0], sMid[1])

	// Gopus: decode at native rate only (no resampling)
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("Gopus DecodeFrame error: %v", err)
	}

	// Libopus: decode at 8kHz
	libNative, libSamples := libDec8k.DecodeFloat(pkt, 320)
	t.Logf("Gopus native samples: %d, Libopus native samples: %d", len(goNative), libSamples)

	// Compare first 20 samples
	minLen := len(goNative)
	if libSamples < minLen {
		minLen = libSamples
	}

	t.Logf("\nNative samples comparison (before resampling):")
	for i := 0; i < 20 && i < minLen; i++ {
		goInt16 := int16(goNative[i] * 32768.0)
		libInt16 := int16(libNative[i] * 32768.0)
		diff := goNative[i] - libNative[i]
		t.Logf("  [%2d] go=%.6f (%5d) lib=%.6f (%5d) diff=%.6f",
			i, goNative[i], goInt16, libNative[i], libInt16, diff)
	}

	// Build what the resampler input WOULD be
	t.Logf("\nResampler input (sMid[1] + native[0:n-1]):")
	resamplerInput := make([]float32, len(goNative))
	resamplerInput[0] = float32(sMid[1]) / 32768.0
	if len(goNative) > 1 {
		copy(resamplerInput[1:], goNative[:len(goNative)-1])
	}
	for i := 0; i < 10 && i < len(resamplerInput); i++ {
		int16Val := int16(resamplerInput[i] * 32768.0)
		t.Logf("  [%2d] %.6f (%5d)", i, resamplerInput[i], int16Val)
	}
}
