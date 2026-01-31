// Package cgo tests SILK decoder parameters at bandwidth transitions.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12BWTransitionParams compares decoded SILK parameters at bandwidth transitions.
func TestTV12BWTransitionParams(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 200)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Create libopus decoder for reference output
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Also create a gopus decoder for full Opus-level comparison
	goDec, _ := gopus.NewDecoder(48000, 1)
	if goDec == nil {
		t.Fatal("Could not create gopus decoder")
	}

	t.Log("=== TV12 Bandwidth Transition Parameter Analysis ===")
	t.Log("Comparing decoded parameters at NB→MB transition (packets 136-137)")

	prevBW := silk.Bandwidth(255)

	for i := 0; i <= 140 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Decode with libopus (updates state)
		libPcm, _ := libDec.DecodeFloat(pkt, 1920)

		// Decode with gopus full decoder
		goPcm, _ := goDec.DecodeFloat32(pkt)

		// Only analyze SILK packets near transition
		if toc.Mode != gopus.ModeSILK {
			prevBW = silk.Bandwidth(255)
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Detect bandwidth change
		isBWChange := silkBW != prevBW && prevBW != silk.Bandwidth(255)
		prevBW = silkBW

		// Only detailed analysis around transition
		if i < 134 || i > 140 {
			// Still decode to update state
			var rd rangecoding.Decoder
			rd.Init(pkt[1:])
			silkDec.DecodeFrame(&rd, silkBW, duration, true)
			continue
		}

		// Decode with our SILK decoder
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
			continue
		}

		// Get decoder state after decode
		state := silkDec.ExportState()
		params := silkDec.GetLastFrameParams()

		// Calculate SNR vs libopus
		minLen := len(goPcm)
		if len(libPcm) < minLen {
			minLen = len(libPcm)
		}
		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := goPcm[j] - libPcm[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j] * libPcm[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		marker := ""
		if isBWChange {
			marker = " <-- BW CHANGE"
		}

		t.Logf("\n=== Packet %d: BW=%v (%dkHz) SNR=%.1f dB%s ===",
			i, silkBW, state.FsKHz, snr, marker)

		t.Logf("  State: lpcOrder=%d, fsKHz=%d, nbSubfr=%d",
			state.LpcOrder, state.FsKHz, params.LagPrev)
		t.Logf("  Params: NLSFInterpCoefQ2=%d, LTPScaleIndex=%d",
			params.NLSFInterpCoefQ2, params.LTPScaleIndex)
		t.Logf("  Gains: %v", params.GainIndices)

		// Show first few native samples
		if len(goNative) >= 10 {
			t.Logf("  Native[0:10]: [%.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f]",
				goNative[0], goNative[1], goNative[2], goNative[3], goNative[4],
				goNative[5], goNative[6], goNative[7], goNative[8], goNative[9])
		}

		// Show first/last sLPC values
		t.Logf("  sLPC[0:4]: [%d, %d, %d, %d]",
			state.SLPCQ14Buf[0], state.SLPCQ14Buf[1], state.SLPCQ14Buf[2], state.SLPCQ14Buf[3])

		// Show outBuf (LTP history)
		if len(state.OutBuf) >= 4 {
			t.Logf("  outBuf[0:4]: [%d, %d, %d, %d]",
				state.OutBuf[0], state.OutBuf[1], state.OutBuf[2], state.OutBuf[3])
		}

		// Show sample comparison
		t.Log("  First 10 samples (go vs lib):")
		for j := 0; j < 10 && j < minLen; j++ {
			ratio := float32(0)
			if libPcm[j] != 0 {
				ratio = goPcm[j] / libPcm[j]
			}
			t.Logf("    [%2d] go=%+9.6f lib=%+9.6f diff=%+9.6f ratio=%.2f",
				j, goPcm[j], libPcm[j], goPcm[j]-libPcm[j], ratio)
		}
	}
}

// TestTV12CompareNativeAtBWChange compares SILK native output vs libopus at 8k/12k rate.
func TestTV12CompareNativeAtBWChange(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 150)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder for native rate
	silkDec := silk.NewDecoder()

	// Create libopus decoders at native rates
	libDec8k, _ := NewLibopusDecoder(8000, 1)
	libDec12k, _ := NewLibopusDecoder(12000, 1)
	if libDec8k == nil || libDec12k == nil {
		t.Skip("Could not create libopus native decoders")
	}
	defer libDec8k.Destroy()
	defer libDec12k.Destroy()

	t.Log("=== Native Rate Comparison at BW Transition ===")

	prevBW := silk.Bandwidth(255)

	for i := 0; i <= 140 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Decode with libopus at native rates (to update state)
		libDec8k.DecodeFloat(pkt, 320)
		libDec12k.DecodeFloat(pkt, 480)

		if toc.Mode != gopus.ModeSILK {
			prevBW = silk.Bandwidth(255)
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		isBWChange := silkBW != prevBW && prevBW != silk.Bandwidth(255)
		prevBW = silkBW

		// Only analyze around transition
		if i < 134 || i > 140 {
			duration := silk.FrameDurationFromTOC(toc.FrameSize)
			var rd rangecoding.Decoder
			rd.Init(pkt[1:])
			silkDec.DecodeFrame(&rd, silkBW, duration, true)
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		// Decode with gopus
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			continue
		}

		// Get reference from appropriate native decoder
		var libDec *LibopusDecoder
		if silkBW == silk.BandwidthNarrowband {
			libDec = libDec8k
		} else {
			libDec = libDec12k
		}
		libNative, libN := libDec.DecodeFloat(pkt, len(goNative)*2)
		if libN <= 0 {
			continue
		}

		// Calculate native-rate SNR
		minLen := len(goNative)
		if libN < minLen {
			minLen = libN
		}
		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := goNative[j] - libNative[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libNative[j] * libNative[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		marker := ""
		if isBWChange {
			marker = " <-- BW CHANGE"
		}

		t.Logf("\nPacket %d: BW=%v (%dkHz) NativeSNR=%.1f dB%s",
			i, silkBW, config.SampleRate/1000, snr, marker)

		if snr < 30 {
			t.Log("  First 10 native samples:")
			for j := 0; j < 10 && j < minLen; j++ {
				ratio := float32(0)
				if libNative[j] != 0 {
					ratio = goNative[j] / libNative[j]
				}
				t.Logf("    [%2d] go=%+9.6f lib=%+9.6f diff=%+9.6f ratio=%.2f",
					j, goNative[j], libNative[j], goNative[j]-libNative[j], ratio)
			}

			// Count sign inversions
			inversions := 0
			for j := 0; j < minLen; j++ {
				if goNative[j] != 0 && libNative[j] != 0 {
					r := goNative[j] / libNative[j]
					if r < -0.5 && r > -2 {
						inversions++
					}
				}
			}
			t.Logf("  Sign inversions (ratio ~-1): %d / %d", inversions, minLen)
		}
	}
}
