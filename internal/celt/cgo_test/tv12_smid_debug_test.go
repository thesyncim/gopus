// Package cgo traces sMid state to understand the bandwidth transition issue.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12SMidAndResamplerDebug traces sMid state and resampler input.
func TestTV12SMidAndResamplerDebug(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create gopus SILK decoder that we'll use for the full decode
	silkDec := silk.NewDecoder()

	t.Log("=== TV12 sMid and Resampler Debug ===")

	// Process packets 0-825 with gopus SILK decoder
	t.Log("Processing packets 0-825 with gopus SILK decoder...")
	prevBW := silk.Bandwidth(255)
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

		// Track sMid changes around bandwidth transitions
		if silkBW != prevBW || i == 825 {
			sMidBefore := silkDec.GetSMid()
			silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
			sMidAfter := silkDec.GetSMid()

			if i >= 134 && i <= 138 || i >= 824 {
				t.Logf("Packet %d: BW=%v->%v, sMid: [%d,%d] -> [%d,%d]",
					i, prevBW, silkBW,
					sMidBefore[0], sMidBefore[1],
					sMidAfter[0], sMidAfter[1])
			}
		} else {
			silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		}
		prevBW = silkBW
	}

	// Get sMid before packet 826
	sMidBeforeGo := silkDec.GetSMid()
	t.Logf("\nGopus sMid before packet 826: [%d, %d]", sMidBeforeGo[0], sMidBeforeGo[1])

	// Decode packet 826 with gopus - get native output
	pkt826 := packets[826]
	toc := gopus.ParseTOC(pkt826[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	var rd rangecoding.Decoder
	rd.Init(pkt826[1:])
	goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus decode error: %v", err)
	}

	// Build resampler input manually
	goResamplerInput := make([]float32, len(goNative))
	goResamplerInput[0] = float32(sMidBeforeGo[1]) / 32768.0
	if len(goNative) > 1 {
		copy(goResamplerInput[1:], goNative[:len(goNative)-1])
	}

	t.Log("\nGopus resampler input (first 10):")
	for j := 0; j < 10 && j < len(goResamplerInput); j++ {
		int16Val := int16(goResamplerInput[j] * 32768.0)
		t.Logf("  [%d] %.6f (%d)", j, goResamplerInput[j], int16Val)
	}

	// Now resample with gopus
	goResampler := silk.NewLibopusResampler(8000, 48000)
	goResampler.Reset()
	goOut := goResampler.Process(goResamplerInput)

	t.Logf("\nGopus resampled output (first 20):")
	for j := 0; j < 20 && j < len(goOut); j++ {
		t.Logf("  [%d] %.6f", j, goOut[j])
	}

	// Now compare with libopus Opus-level decoder
	t.Log("\n=== Comparing with libopus at Opus level ===")
	goDec, _ := gopus.NewDecoder(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process packets 0-825 through Opus decoders
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		goDec.DecodeFloat32(pkt)
		libDec.DecodeFloat(pkt, 1920)
	}

	// Decode packet 826
	goOpusOut, _ := goDec.DecodeFloat32(pkt826)
	libOpusOut, libN := libDec.DecodeFloat(pkt826, 1920)

	t.Log("\n48kHz output comparison for packet 826:")
	minLen := len(goOpusOut)
	if libN < minLen {
		minLen = libN
	}

	t.Log("First 20 samples:")
	for j := 0; j < 20 && j < minLen; j++ {
		ratio := float32(0)
		if libOpusOut[j] != 0 {
			ratio = goOpusOut[j] / libOpusOut[j]
		}
		t.Logf("  [%2d] go=%+9.6f lib=%+9.6f ratio=%.3f", j, goOpusOut[j], libOpusOut[j], ratio)
	}

	// Calculate SNR
	var sumSqErr, sumSqSig float64
	for j := 0; j < minLen; j++ {
		diff := goOpusOut[j] - libOpusOut[j]
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(libOpusOut[j] * libOpusOut[j])
	}
	snr := 10 * math.Log10(sumSqSig/sumSqErr)
	if math.IsNaN(snr) || math.IsInf(snr, 1) {
		snr = 999.0
	}
	t.Logf("\nSNR: %.1f dB", snr)
}
