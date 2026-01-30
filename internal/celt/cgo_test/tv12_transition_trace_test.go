// Package cgo traces outputs around bandwidth transition
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12TransitionTrace traces SILK output around the MB->NB transition.
func TestTV12TransitionTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Create libopus decoder at 48kHz (API rate) - standard Opus output rate
	libDec48k, _ := NewLibopusDecoder(48000, 1)
	if libDec48k == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec48k.Destroy()

	t.Log("Tracing packets 823-828 (MB->NB transition at 826)")

	// Process packets 0-828
	for i := 0; i <= 828; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Decode with gopus SILK decoder (native rate)
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			continue
		}

		// Decode with libopus at 48kHz
		expectedSamples := (len(goNative) * 48000) / silk.GetBandwidthConfig(silkBW).SampleRate
		libPcm48k, libSamples := libDec48k.DecodeFloat(pkt, expectedSamples*2)

		// Only trace packets 823-828
		if i < 823 || i > 828 {
			continue
		}

		// Downsample libopus output to native rate for comparison
		nativeRate := silk.GetBandwidthConfig(silkBW).SampleRate
		resampleRatio := 48000 / nativeRate

		minLen := len(goNative)
		if libSamples/resampleRatio < minLen {
			minLen = libSamples / resampleRatio
		}

		// Calculate SNR
		var sumSqErr, sumSqSig float64
		var maxDiff float32
		maxDiffIdx := 0

		for j := 0; j < minLen; j++ {
			// Simple downsampling - take every Nth sample
			libSample := libPcm48k[j*resampleRatio]
			diff := goNative[j] - libSample
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libSample * libSample)
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
				maxDiffIdx = j
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		t.Logf("\n=== Packet %d: BW=%s, Native=%dHz ===", i, silkBW, nativeRate)
		t.Logf("Go samples: %d, Lib samples: %d (at 48kHz), comparing: %d", len(goNative), libSamples, minLen)
		t.Logf("SNR: %.1f dB, MaxDiff: %.6f at sample %d", snr, maxDiff, maxDiffIdx)

		// Dump first 20 samples
		t.Logf("First 20 samples (gopus native vs libopus downsampled):")
		for j := 0; j < 20 && j < minLen; j++ {
			libSample := libPcm48k[j*resampleRatio]
			t.Logf("  [%3d] go=%+9.6f lib=%+9.6f diff=%+9.6f", j, goNative[j], libSample, goNative[j]-libSample)
		}
	}
}
