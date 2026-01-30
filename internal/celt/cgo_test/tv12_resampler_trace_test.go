// Package cgo traces resampler state at bandwidth transition.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ResamplerTrace traces resampler state across BW transition.
func TestTV12ResamplerTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus decoder
	goDec, _ := gopus.NewDecoder(48000, 1)
	silkDec := goDec.GetSILKDecoder()

	t.Log("=== Resampler State at BW Transition ===")

	for i := 0; i < 145 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			goDec.DecodeFloat32(pkt)
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		// Get resampler BEFORE decode
		resampler := silkDec.GetResampler(silkBW)

		// Only log around transition
		if i >= 134 && i <= 140 {
			marker := ""
			if i == 137 {
				marker = " <-- BW CHANGE"
			}

			t.Logf("\nPacket %d: BW=%v%s", i, silkBW, marker)
			if resampler != nil {
				sIIR := resampler.GetSIIR()
				sFIR := resampler.GetSFIR()
				delayBuf := resampler.GetDelayBuf()
				t.Logf("  BEFORE decode:")
				t.Logf("    sIIR[0:3] = [%d, %d, %d]", sIIR[0], sIIR[1], sIIR[2])
				t.Logf("    sFIR[0:3] = [%d, %d, %d]", sFIR[0], sFIR[1], sFIR[2])
				if len(delayBuf) >= 3 {
					t.Logf("    delayBuf[0:3] = [%d, %d, %d]", delayBuf[0], delayBuf[1], delayBuf[2])
				}
			} else {
				t.Log("  Resampler not created yet")
			}
		}

		// Decode
		goSamples, _ := goDec.DecodeFloat32(pkt)

		// Get resampler AFTER decode
		if i >= 134 && i <= 140 {
			resamplerAfter := silkDec.GetResampler(silkBW)
			if resamplerAfter != nil {
				sIIR := resamplerAfter.GetSIIR()
				sFIR := resamplerAfter.GetSFIR()
				delayBuf := resamplerAfter.GetDelayBuf()
				t.Logf("  AFTER decode:")
				t.Logf("    sIIR[0:3] = [%d, %d, %d]", sIIR[0], sIIR[1], sIIR[2])
				t.Logf("    sFIR[0:3] = [%d, %d, %d]", sFIR[0], sFIR[1], sFIR[2])
				if len(delayBuf) >= 3 {
					t.Logf("    delayBuf[0:3] = [%d, %d, %d]", delayBuf[0], delayBuf[1], delayBuf[2])
				}
			}

			// Show first few output samples
			if len(goSamples) >= 5 {
				t.Logf("  Output[0:5] = [%.6f, %.6f, %.6f, %.6f, %.6f]",
					goSamples[0], goSamples[1], goSamples[2], goSamples[3], goSamples[4])
			}
		}
	}
}

// TestTV12NoResamplerReset tests without resetting resampler on BW change.
func TestTV12NoResamplerReset(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus decoder
	goDec, _ := gopus.NewDecoder(48000, 1)
	silkDec := goDec.GetSILKDecoder()
	silkDec.SetDisableResamplerReset(true) // Disable reset

	// Create libopus decoder
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Testing without resampler reset ===")

	for i := 0; i < 145 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goSamples, _ := goDec.DecodeFloat32(pkt)
		libPcm, libN := libDec.DecodeFloat(pkt, len(goSamples)*2)

		if i >= 135 && i <= 140 {
			minLen := len(goSamples)
			if libN < minLen {
				minLen = libN
			}

			var sumSqErr, sumSqSig float64
			for j := 0; j < minLen; j++ {
				diff := goSamples[j] - libPcm[j]
				sumSqErr += float64(diff * diff)
				sumSqSig += float64(libPcm[j] * libPcm[j])
			}
			snr := 10 * math.Log10(sumSqSig/sumSqErr)
			if math.IsNaN(snr) || math.IsInf(snr, 1) {
				snr = 999.0
			}

			marker := ""
			if i == 137 {
				marker = " <-- BW CHANGE"
			}
			t.Logf("Packet %d: BW=%d SNR=%.1f dB (no reset)%s", i, toc.Bandwidth, snr, marker)
		}
	}
}
