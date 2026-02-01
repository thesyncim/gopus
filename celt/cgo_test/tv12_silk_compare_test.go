//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// TestTV12SilkPacketsCompare compares SILK packets from TV12 against libopus.
// This targets the worst SILK packets identified: 826, 137, 758, 1118.
func TestTV12SilkPacketsCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	// Load more packets to reach packet 826
	packets, err := loadPacketsSimple(bitFile, 850)
	if err != nil {
		t.Skip("Could not load packets")
	}
	t.Logf("Loaded %d packets", len(packets))

	// Worst packets from divergence analysis
	worstPackets := []int{826, 213, 137, 758}

	signalTypes := []string{"inactive", "unvoiced", "voiced"}

	// Create persistent decoders to maintain state
	goDec := silk.NewDecoder()

	libDec, _ := NewLibopusDecoder(16000, 1) // WB rate for SILK
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	prevSilkBW := silk.BandwidthNarrowband

	for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue // Skip non-SILK packets
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		// Reset decoders if bandwidth changes
		if silkBW != prevSilkBW {
			goDec.Reset()
			libDec.Destroy()
			libDec, _ = NewLibopusDecoder(config.SampleRate, 1)
			if libDec == nil {
				continue
			}
		}
		prevSilkBW = silkBW

		delay := 5
		if config.SampleRate == 16000 {
			delay = 13
		} else if config.SampleRate == 12000 {
			delay = 10
		}

		// Go decode
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("Packet %2d: gopus error: %v", pktIdx, err)
			continue
		}

		sigType := goDec.GetLastSignalType()
		sigName := signalTypes[sigType]

		// Libopus decode
		libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
		if libSamples < 0 {
			continue
		}

		alignedLen := len(goNative)
		if libSamples-delay < alignedLen {
			alignedLen = libSamples - delay
		}

		var sumSqErr, sumSqSig float64
		var maxDiff float32
		maxDiffIdx := 0
		for i := 0; i < alignedLen; i++ {
			goVal := goNative[i]
			libVal := libPcm[i+delay]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
				maxDiffIdx = i
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// Check if this is one of the worst packets
		isWorst := false
		for _, w := range worstPackets {
			if pktIdx == w {
				isWorst = true
				break
			}
		}

		if isWorst {
			t.Logf("\n=== Packet %d (WORST) ===", pktIdx)
			t.Logf("Mode: %v, BW: %s, Duration: %v, SignalType: %s",
				toc.Mode, silkBW, duration, sigName)
			t.Logf("Native SNR: %.1f dB, MaxDiff: %.6f at sample %d",
				snr, maxDiff, maxDiffIdx)

			// Show samples around max diff
			t.Logf("Samples around max diff:")
			start := maxDiffIdx - 5
			if start < 0 {
				start = 0
			}
			end := maxDiffIdx + 6
			if end > alignedLen {
				end = alignedLen
			}
			for i := start; i < end; i++ {
				marker := ""
				if i == maxDiffIdx {
					marker = " <-- MAX"
				}
				t.Logf("  [%3d] go=%.6f lib=%.6f diff=%.6f%s",
					i, goNative[i], libPcm[i+delay], goNative[i]-libPcm[i+delay], marker)
			}

			// Show first 10 samples
			t.Logf("First 10 samples:")
			for i := 0; i < 10 && i < alignedLen; i++ {
				t.Logf("  [%3d] go=%.6f lib=%.6f diff=%.6f",
					i, goNative[i], libPcm[i+delay], goNative[i]-libPcm[i+delay])
			}
		} else if snr < 40 {
			t.Logf("Packet %3d %-9s: SNR=%6.1f dB, MaxDiff=%.4f at [%d]",
				pktIdx, sigName, snr, maxDiff, maxDiffIdx)
		}
	}
}

// TestTV12Packet826NativeCompare does detailed comparison for packet 826
func TestTV12Packet826NativeCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Process packets up to and including 826 to build state
	targetIdx := 826

	goDec := silk.NewDecoder()

	// Track libopus decoder with the right sample rate
	var libDec *LibopusDecoder
	var prevSampleRate int

	for pktIdx := 0; pktIdx <= targetIdx; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		// Create/recreate libopus decoder if sample rate changes
		if config.SampleRate != prevSampleRate {
			if libDec != nil {
				libDec.Destroy()
			}
			libDec, _ = NewLibopusDecoder(config.SampleRate, 1)
			prevSampleRate = config.SampleRate
		}
		if libDec == nil {
			continue
		}

		// Go decode
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			continue
		}

		// Libopus decode
		libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
		if libSamples < 0 {
			continue
		}

		if pktIdx == targetIdx {
			delay := 5 // NB delay
			if config.SampleRate == 16000 {
				delay = 13
			} else if config.SampleRate == 12000 {
				delay = 10
			}

			t.Logf("Packet %d: BW=%s, Duration=%v, GoSamples=%d, LibSamples=%d, Delay=%d",
				pktIdx, silkBW, duration, len(goNative), libSamples, delay)

			alignedLen := len(goNative)
			if libSamples-delay < alignedLen {
				alignedLen = libSamples - delay
			}

			t.Logf("First 30 samples comparison (go vs lib at native rate):")
			for i := 0; i < 30 && i < alignedLen; i++ {
				goVal := goNative[i]
				libVal := libPcm[i+delay]
				diff := goVal - libVal
				t.Logf("  [%3d] go=%10.6f lib=%10.6f diff=%10.6f",
					i, goVal, libVal, diff)
			}

			// Find where sign inverts
			t.Logf("\nLooking for sign inversion points:")
			for i := 0; i < alignedLen-1; i++ {
				goVal := goNative[i]
				libVal := libPcm[i+delay]
				goPrev := float32(0)
				libPrev := float32(0)
				if i > 0 {
					goPrev = goNative[i-1]
					libPrev = libPcm[i-1+delay]
				}

				// Check if go and lib have opposite signs
				if (goVal > 0.001 && libVal < -0.001) || (goVal < -0.001 && libVal > 0.001) {
					t.Logf("  Sign inversion at [%3d]: go=%.6f lib=%.6f (prev: go=%.6f lib=%.6f)",
						i, goVal, libVal, goPrev, libPrev)
					if i > 100 {
						break // Stop after first few inversions
					}
				}
			}
		}
	}

	if libDec != nil {
		libDec.Destroy()
	}
}
