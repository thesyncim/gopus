//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// TestPacket15IndicesCompare compares decoded indices between gopus and libopus for packet 15.
func TestPacket15IndicesCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil || len(packets) < 16 {
		t.Skip("Could not load packets")
	}

	pkt := packets[15]
	toc := gopus.ParseTOC(pkt[0])

	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	config := silk.GetBandwidthConfig(silkBW)

	fsKHz := config.SampleRate / 1000
	nbSubfr := 4
	framesPerPacket := 3
	frameLength := 160

	t.Log("Comparing decoded indices for packet 15:")

	// Compare each frame
	for frame := 0; frame < framesPerPacket; frame++ {
		// Get libopus decoded indices
		libIndices, err := SilkDecodeIndicesPulses(pkt[1:], fsKHz, nbSubfr, framesPerPacket, frame, frameLength)
		if err != nil || libIndices == nil {
			t.Logf("  Frame %d: Could not decode with libopus", frame)
			continue
		}

		t.Logf("\n=== Frame %d ===", frame)
		t.Logf("  libopus: SignalType=%d, NLSFInterpCoef=%d, QuantOffset=%d",
			libIndices.SignalType, libIndices.NLSFInterpCoef, libIndices.QuantOffsetType)
		t.Logf("  libopus: GainsIndices=%v", libIndices.GainsIndices[:nbSubfr])
		t.Logf("  libopus: LagIndex=%d, ContourIndex=%d", libIndices.LagIndex, libIndices.ContourIndex)
		t.Logf("  libopus: LTPIndex=%v", libIndices.LTPIndex[:nbSubfr])
		t.Logf("  libopus: PERIndex=%d, LTPScaleIndex=%d, Seed=%d",
			libIndices.PERIndex, libIndices.LTPScaleIndex, libIndices.Seed)

		// Show first few pulses
		t.Logf("  libopus: Pulses[0:20]=%v", libIndices.Pulses[:20])
	}

	// Now also decode with gopus and compare
	t.Log("\n=== gopus decoded parameters ===")
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()

	goOutput, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}
	t.Logf("gopus output samples: %d", len(goOutput))

	// The gopus decoder doesn't expose per-frame indices easily
	// Let's just show the final state
	params := goDec.GetLastFrameParams()
	t.Logf("  gopus final: NLSFInterpCoefQ2=%d, LTPScaleIndex=%d, LagPrev=%d",
		params.NLSFInterpCoefQ2, params.LTPScaleIndex, params.LagPrev)
	t.Logf("  gopus final: GainIndices=%v", params.GainIndices)
}

// TestCompareIndicesAllFrames compares all frames for packets 4 and 15.
func TestCompareIndicesAllFrames(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil {
		t.Skip("Could not load packets")
	}

	for _, pktIdx := range []int{4, 15} {
		if pktIdx >= len(packets) {
			continue
		}
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])

		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		config := silk.GetBandwidthConfig(silkBW)

		fsKHz := config.SampleRate / 1000
		nbSubfr := 4
		framesPerPacket := 3
		frameLength := 160

		t.Logf("\n============ Packet %d ============", pktIdx)

		for frame := 0; frame < framesPerPacket; frame++ {
			libIndices, err := SilkDecodeIndicesPulses(pkt[1:], fsKHz, nbSubfr, framesPerPacket, frame, frameLength)
			if err != nil || libIndices == nil {
				continue
			}

			signalTypes := []string{"inactive", "unvoiced", "voiced"}
			sigName := signalTypes[libIndices.SignalType]

			interpFlag := libIndices.NLSFInterpCoef < 4

			t.Logf("  Frame %d: signal=%s, NLSFInterp=%d (interpFlag=%v)",
				frame, sigName, libIndices.NLSFInterpCoef, interpFlag)
		}
	}
}
