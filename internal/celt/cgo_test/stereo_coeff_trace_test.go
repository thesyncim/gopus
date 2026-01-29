// Package cgo traces stereo coefficient decoding for packet 14
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTracePacket14MidSide traces the M/S values using high-level decoder
func TestTracePacket14MidSide(t *testing.T) {
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

	// Create persistent decoders to match how compliance test works
	goDecHL, _ := gopus.NewDecoder(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	// Decode all packets up through 14 to build proper state
	for pktIdx := 0; pktIdx <= 14; pktIdx++ {
		pkt := packets[pktIdx]

		// Decode with gopus using high-level decoder
		goSamplesF32, _ := goDecHL.DecodeFloat32(pkt)

		// Decode with libopus
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		if pktIdx < 13 {
			continue
		}

		t.Logf("\n=== Packet %d (len=%d) ===", pktIdx, len(pkt))

		// Convert gopus to float64
		goSamples := make([]float64, len(goSamplesF32))
		for i, v := range goSamplesF32 {
			goSamples[i] = float64(v)
		}

		// Analyze M/S pattern in output
		t.Log("Analyzing Mid/Side pattern:")

		var mDiffSum, sDiffSum float64
		var mDiffSq, sDiffSq float64
		for i := 0; i < libSamples && i*2+1 < len(goSamples); i++ {
			libL := float64(libPcm[i*2])
			libR := float64(libPcm[i*2+1])
			goL := goSamples[i*2]
			goR := goSamples[i*2+1]

			libM := (libL + libR) / 2
			libS := (libL - libR) / 2
			goM := (goL + goR) / 2
			goS := (goL - goR) / 2

			mDiff := goM - libM
			sDiff := goS - libS

			mDiffSum += mDiff
			sDiffSum += sDiff
			mDiffSq += mDiff * mDiff
			sDiffSq += sDiff * sDiff
		}

		avgMDiff := mDiffSum / float64(libSamples)
		avgSDiff := sDiffSum / float64(libSamples)
		rmsMDiff := math.Sqrt(mDiffSq / float64(libSamples))
		rmsSDiff := math.Sqrt(sDiffSq / float64(libSamples))

		t.Logf("  Mid diff:  avg=%+.2e, rms=%.2e", avgMDiff, rmsMDiff)
		t.Logf("  Side diff: avg=%+.2e, rms=%.2e", avgSDiff, rmsSDiff)

		// Check if M and S diffs have opposite signs
		if pktIdx == 14 {
			t.Log("\n  Checking if diff_M ≈ -diff_S:")
			for i := 0; i < 10 && i*2+1 < len(goSamples); i++ {
				libL := float64(libPcm[i*2])
				libR := float64(libPcm[i*2+1])
				goL := goSamples[i*2]
				goR := goSamples[i*2+1]

				libM := (libL + libR) / 2
				libS := (libL - libR) / 2
				goM := (goL + goR) / 2
				goS := (goL - goR) / 2

				mDiff := goM - libM
				sDiff := goS - libS
				ratio := 0.0
				if sDiff != 0 {
					ratio = mDiff / sDiff
				}
				t.Logf("    [%d] M_diff=%+.2e, S_diff=%+.2e, ratio=%.4f", i, mDiff, sDiff, ratio)
			}

			// Compute SNR for L and R
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
			t.Logf("\n  SNR: L=%.1f dB, R=%.1f dB", lSNR, rSNR)
		}
	}
}

// TestTraceErrorGrowth shows how error grows through packet 14
func TestTraceErrorGrowth(t *testing.T) {
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

	// Use synced decoders (decode 0-13 first)
	goDecHL, _ := gopus.NewDecoder(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	// Sync up to packet 13
	for i := 0; i < 14; i++ {
		goDecHL.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode packet 14
	pkt := packets[14]
	t.Logf("Packet 14: len=%d, TOC=0x%02X", len(pkt), pkt[0])

	goSamplesF32, _ := goDecHL.DecodeFloat32(pkt)
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

	t.Logf("Go samples: %d, Lib samples: %d", len(goSamplesF32)/2, libSamples)

	// Convert to float64
	goSamples := make([]float64, len(goSamplesF32))
	for i, v := range goSamplesF32 {
		goSamples[i] = float64(v)
	}

	t.Log("\nError growth through frame (every 20 samples):")
	for i := 0; i < libSamples && i*2+1 < len(goSamples); i += 20 {
		libL := float64(libPcm[i*2])
		libR := float64(libPcm[i*2+1])
		goL := goSamples[i*2]
		goR := goSamples[i*2+1]

		lDiff := goL - libL
		rDiff := goR - libR

		t.Logf("  [%3d] L_diff=%+.2e, R_diff=%+.2e", i, lDiff, rDiff)
	}

	// Find max error
	maxRDiff := 0.0
	maxRIdx := 0
	for i := 0; i < libSamples && i*2+1 < len(goSamples); i++ {
		libR := float64(libPcm[i*2+1])
		goR := goSamples[i*2+1]
		rDiff := math.Abs(goR - libR)
		if rDiff > maxRDiff {
			maxRDiff = rDiff
			maxRIdx = i
		}
	}
	t.Logf("\nMax R error at sample %d: diff=%.6f", maxRIdx, maxRDiff)
}
