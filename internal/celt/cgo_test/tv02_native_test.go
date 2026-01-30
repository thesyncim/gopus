// Package cgo tests TV02 SILK decoder at native 8kHz rate.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV02NativeRate tests SILK decoder against libopus at 8kHz.
func TestTV02NativeRate(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"

	packets, err := loadPacketsSimple(bitFile, 100)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder for native rate
	silkDec := silk.NewDecoder()

	// Create libopus decoder at 8kHz native rate
	libDec8k, _ := NewLibopusDecoder(8000, 1)
	if libDec8k == nil {
		t.Skip("Could not create 8k libopus decoder")
	}
	defer libDec8k.Destroy()

	t.Log("=== TV02 Native 8kHz Comparison ===")

	failCount := 0
	for i := 0; i < len(packets) && i < 100; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok || silkBW != silk.BandwidthNarrowband {
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Decode with gopus SILK
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
			continue
		}

		// Decode with libopus at 8kHz
		libNative, libN := libDec8k.DecodeFloat(pkt, 320)
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

		// Check max amplitude
		var maxAbs float32
		for j := 0; j < minLen; j++ {
			v := libNative[j]
			if v < 0 {
				v = -v
			}
			if v > maxAbs {
				maxAbs = v
			}
		}

		// Only log substantial packets with poor SNR
		if maxAbs > 0.01 && snr < 40 {
			failCount++
			t.Logf("Packet %3d: NativeSNR=%6.1f dB (maxAbs=%.4f) [FAIL]", i, snr, maxAbs)
			if failCount <= 3 {
				t.Log("  First 10 samples:")
				for j := 0; j < 10 && j < minLen; j++ {
					t.Logf("    [%2d] go=%+10.6f lib=%+10.6f diff=%+10.6f",
						j, goNative[j], libNative[j], goNative[j]-libNative[j])
				}
			}
		} else if i < 10 || i%20 == 0 {
			t.Logf("Packet %3d: NativeSNR=%6.1f dB (maxAbs=%.4f) [OK]", i, snr, maxAbs)
		}
	}

	if failCount > 0 {
		t.Errorf("Found %d failing packets in TV02", failCount)
	} else {
		t.Log("All TV02 packets pass native rate comparison!")
	}
}
