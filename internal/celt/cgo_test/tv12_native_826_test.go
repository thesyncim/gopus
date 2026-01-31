// Package cgo checks native SILK output at packet 826.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Native826 compares native SILK output at packet 826.
func TestTV12Native826(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus SILK decoder
	silkDec := silk.NewDecoder()

	// Create libopus decoder at 48kHz
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Processing packets 0-825 to build state ===")

	// Process packets with both decoders
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// libopus at 48kHz
		libDec.DecodeFloat(pkt, 1920)

		// gopus SILK at native rate (skip non-SILK)
		if toc.Mode == gopus.ModeSILK {
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if ok {
				duration := silk.FrameDurationFromTOC(toc.FrameSize)
				var rd rangecoding.Decoder
				rd.Init(pkt[1:])
				silkDec.DecodeFrame(&rd, silkBW, duration, true)
			}
		}
	}

	t.Log("=== Comparing native output at packet 826 (NB) ===")

	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	config := silk.GetBandwidthConfig(silkBW)
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	t.Logf("Packet 826: Mode=%v BW=%d (%s), native rate=%d Hz",
		toc.Mode, toc.Bandwidth, []string{"NB", "MB", "WB"}[silkBW], config.SampleRate)

	// Gopus: decode at native rate
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("Gopus decode error: %v", err)
	}

	// Libopus: decode at 48kHz
	libOut, libN := libDec.DecodeFloat(pkt, 1920)

	t.Logf("Gopus native samples: %d (at %d Hz)", len(goNative), config.SampleRate)
	t.Logf("Libopus 48kHz samples: %d", libN)

	// Check if native samples are all zero
	allZero := true
	maxAbs := float32(0)
	for _, s := range goNative {
		if s != 0 {
			allZero = false
		}
		if abs := float32(math.Abs(float64(s))); abs > maxAbs {
			maxAbs = abs
		}
	}
	t.Logf("Gopus native: allZero=%v, maxAbs=%.6f", allZero, maxAbs)

	// Check libopus samples
	libAllZero := true
	libMaxAbs := float32(0)
	for i := 0; i < libN; i++ {
		if libOut[i] != 0 {
			libAllZero = false
		}
		if abs := float32(math.Abs(float64(libOut[i]))); abs > libMaxAbs {
			libMaxAbs = abs
		}
	}
	t.Logf("Libopus 48kHz: allZero=%v, maxAbs=%.6f", libAllZero, libMaxAbs)

	// Show first 20 native samples
	t.Log("\nGopus native samples (first 20):")
	for i := 0; i < 20 && i < len(goNative); i++ {
		t.Logf("  [%2d] %+.6f", i, goNative[i])
	}

	t.Log("\nLibopus 48kHz samples (first 20):")
	for i := 0; i < 20 && i < libN; i++ {
		t.Logf("  [%2d] %+.6f", i, libOut[i])
	}
}
