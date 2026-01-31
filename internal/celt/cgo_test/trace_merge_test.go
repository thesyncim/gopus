// Package cgo traces stereo merge in detail
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestAnalyzeStereoErrorPattern analyzes where the M/S error pattern originates
func TestAnalyzeStereoErrorPattern(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector08.bit"
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	// Use synced decoders
	goDecHL, _ := gopus.NewDecoder(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	// Sync up to packet 13
	for i := 0; i < 14; i++ {
		goDecHL.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Decode packet 14
	pkt := packets[14]
	t.Logf("Packet 14: len=%d, TOC=0x%02X", len(pkt), pkt[0])

	goSamplesF32, _ := goDecHL.DecodeFloat32(pkt)
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

	// Convert to float64
	goSamples := make([]float64, len(goSamplesF32))
	for i, v := range goSamplesF32 {
		goSamples[i] = float64(v)
	}

	// Analyze error pattern more carefully
	// If M/S coeff errors are SAME sign, then after merge:
	//   L error = (mid-1)*ε ≈ 0 (for mid ≈ 1)
	//   R error = (mid+1)*ε ≈ 2ε
	// If M/S coeff errors are OPPOSITE sign:
	//   L error = (mid+1)*ε ≈ 2ε
	//   R error = (mid-1)*ε ≈ 0

	t.Log("\nDetailed error analysis:")

	// Look at samples where error is significant
	var sumLDiff, sumRDiff float64
	var sumLDiffSq, sumRDiffSq float64

	for i := 0; i < libSamples && i*2+1 < len(goSamples); i++ {
		libL := float64(libPcm[i*2])
		libR := float64(libPcm[i*2+1])
		goL := goSamples[i*2]
		goR := goSamples[i*2+1]

		lDiff := goL - libL
		rDiff := goR - libR

		sumLDiff += lDiff
		sumRDiff += rDiff
		sumLDiffSq += lDiff * lDiff
		sumRDiffSq += rDiff * rDiff
	}

	avgLDiff := sumLDiff / float64(libSamples)
	avgRDiff := sumRDiff / float64(libSamples)
	rmsLDiff := math.Sqrt(sumLDiffSq / float64(libSamples))
	rmsRDiff := math.Sqrt(sumRDiffSq / float64(libSamples))

	t.Logf("  L channel: avg_diff=%+.4e, rms_diff=%.4e", avgLDiff, rmsLDiff)
	t.Logf("  R channel: avg_diff=%+.4e, rms_diff=%.4e", avgRDiff, rmsRDiff)
	t.Logf("  Ratio (R_rms/L_rms): %.1f", rmsRDiff/rmsLDiff)

	// The observation is: L error is very small, R error is much larger
	// This means the M/S coefficient errors are SAME sign, and mid ≈ 1

	// Check the actual error values at specific samples
	t.Log("\nSample-by-sample analysis (first 20):")
	for i := 0; i < 20 && i*2+1 < len(goSamples); i++ {
		libL := float64(libPcm[i*2])
		libR := float64(libPcm[i*2+1])
		goL := goSamples[i*2]
		goR := goSamples[i*2+1]

		lDiff := goL - libL
		rDiff := goR - libR

		// If stereoMerge was: L = mid*M - S, R = mid*M + S
		// And coefficient errors are ε_M and ε_S (same sign for both)
		// Then: L_diff = mid*ε_M - ε_S, R_diff = mid*ε_M + ε_S
		// So: ε_M = (L_diff + R_diff) / (2*mid)
		//     ε_S = (R_diff - L_diff) / 2

		// Assuming mid ≈ 1:
		estMCoeffErr := (lDiff + rDiff) / 2
		estSCoeffErr := (rDiff - lDiff) / 2

		t.Logf("  [%2d] L_diff=%+.2e R_diff=%+.2e | est_M_coeff_err=%+.2e est_S_coeff_err=%+.2e",
			i, lDiff, rDiff, estMCoeffErr, estSCoeffErr)
	}

	// Summary
	t.Log("\n=== SUMMARY ===")
	t.Logf("The pattern L_perfect/R_bad indicates that M and S coefficient errors")
	t.Logf("have the SAME sign, causing them to add up in R and cancel in L.")
	t.Logf("This is consistent with a systematic error in energy or gain calculation")
	t.Logf("that affects both channels equally in the same direction.")
}

// TestComparePacket13vs14 compares what's different between good and bad packets
func TestComparePacket13vs14(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector08.bit"
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	// Decode with fresh decoders each time to isolate packet-specific issues
	for pktIdx := 13; pktIdx <= 14; pktIdx++ {
		pkt := packets[pktIdx]
		t.Logf("\n=== Packet %d (len=%d, TOC=0x%02X) ===", pktIdx, len(pkt), pkt[0])

		// Fresh decoders
		goDecHL, _ := gopus.NewDecoder(48000, 2)
		libDec, _ := NewLibopusDecoder(48000, 2)

		goSamplesF32, _ := goDecHL.DecodeFloat32(pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		libDec.Destroy()

		// Convert
		goSamples := make([]float64, len(goSamplesF32))
		for i, v := range goSamplesF32 {
			goSamples[i] = float64(v)
		}

		// Compute SNR
		var lSigPow, lNoisePow, rSigPow, rNoisePow float64
		for i := 0; i < libSamples && i*2+1 < len(goSamples); i++ {
			libL := float64(libPcm[i*2])
			libR := float64(libPcm[i*2+1])
			goL := goSamples[i*2]
			goR := goSamples[i*2+1]

			lSigPow += libL * libL
			lNoisePow += (goL - libL) * (goL - libL)
			rSigPow += libR * libR
			rNoisePow += (goR - libR) * (goR - libR)
		}

		lSNR := 10 * math.Log10(lSigPow/lNoisePow)
		rSNR := 10 * math.Log10(rSigPow/rNoisePow)

		if math.IsInf(lSNR, 1) {
			lSNR = 999
		}
		if math.IsInf(rSNR, 1) {
			rSNR = 999
		}

		t.Logf("  Fresh decoder SNR: L=%.1f dB, R=%.1f dB", lSNR, rSNR)

		// Show first 5 samples
		t.Log("  First 5 samples:")
		for i := 0; i < 5 && i < libSamples; i++ {
			libL := float64(libPcm[i*2])
			libR := float64(libPcm[i*2+1])
			goL := goSamples[i*2]
			goR := goSamples[i*2+1]
			t.Logf("    [%d] L: lib=%.6f go=%.6f diff=%+.2e | R: lib=%.6f go=%.6f diff=%+.2e",
				i, libL, goL, goL-libL, libR, goR, goR-libR)
		}

		// Show raw bytes
		t.Logf("  Raw bytes: %02X", pkt[:minInt(20, len(pkt))])
	}
}
