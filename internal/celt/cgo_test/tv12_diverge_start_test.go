// Package cgo finds the first diverging packet in TV12.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12FindFirstDivergence finds the first packet where native SILK diverges from libopus.
func TestTV12FindFirstDivergence(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 200)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder for native rate
	silkDec := silk.NewDecoder()

	// Create libopus decoders at native rates
	libDec8k, _ := NewLibopusDecoder(8000, 1)
	if libDec8k == nil {
		t.Skip("Could not create 8k libopus decoder")
	}
	defer libDec8k.Destroy()

	t.Log("=== Finding First Divergence in TV12 ===")

	firstBadPacket := -1

	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Decode with libopus at 8kHz to update state
		libNative, libN := libDec8k.DecodeFloat(pkt, 320)

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		// Only compare NB packets since we're using 8kHz decoder
		if silkBW != silk.BandwidthNarrowband {
			// Still decode to update state
			duration := silk.FrameDurationFromTOC(toc.FrameSize)
			var rd rangecoding.Decoder
			rd.Init(pkt[1:])
			silkDec.DecodeFrame(&rd, silkBW, duration, true)
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

		// Check if signal is substantial
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

		status := "OK"
		// Only flag as bad if signal is substantial (maxAbs > 0.01) and SNR is poor
		if maxAbs > 0.01 && (snr < 30 || inversions > 5) {
			status = "BAD"
			if firstBadPacket == -1 {
				firstBadPacket = i
			}
		}

		// Log status for first 50 packets and bad packets
		if i < 50 || status == "BAD" {
			t.Logf("Packet %3d: NativeSNR=%6.1f dB, signInv=%3d/%3d [%s]",
				i, snr, inversions, minLen, status)
		}

		// Once we find first bad, show details
		if status == "BAD" && i == firstBadPacket {
			t.Logf("\n=== First Bad Packet: %d ===", i)
			t.Log("First 20 samples:")
			for j := 0; j < 20 && j < minLen; j++ {
				ratio := float32(0)
				if libNative[j] != 0 {
					ratio = goNative[j] / libNative[j]
				}
				t.Logf("  [%2d] go=%+10.6f lib=%+10.6f diff=%+10.6f ratio=%+.3f",
					j, goNative[j], libNative[j], goNative[j]-libNative[j], ratio)
			}

			// Get decoder state
			state := silkDec.ExportState()
			t.Logf("  sLPC[0:4] = [%d, %d, %d, %d]",
				state.SLPCQ14Buf[0], state.SLPCQ14Buf[1], state.SLPCQ14Buf[2], state.SLPCQ14Buf[3])
			if len(state.OutBuf) >= 4 {
				t.Logf("  outBuf[0:4] = [%d, %d, %d, %d]",
					state.OutBuf[0], state.OutBuf[1], state.OutBuf[2], state.OutBuf[3])
			}
			break
		}
	}

	if firstBadPacket == -1 {
		t.Log("No divergence found in first 200 NB packets!")
	} else {
		t.Logf("\nFirst diverging NB packet: %d", firstBadPacket)
	}
}
