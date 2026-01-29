// Package cgo traces stereoMerge parameters to find the source of identical M/S errors
package cgo

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTraceStereoMergeParams traces the mid/side values and lgain/rgain to find discrepancy
func TestTraceStereoMergeParams(t *testing.T) {
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

	// Test with fresh decoders to isolate packet-specific issues
	pkt := packets[14]
	t.Logf("Testing packet 14: len=%d, TOC=0x%02X", len(pkt), pkt[0])

	// Decode with fresh gopus decoder
	goDec, _ := gopus.NewDecoder(48000, 2)
	goSamplesF32, _ := goDec.DecodeFloat32(pkt)

	// Decode with fresh libopus decoder
	libDec, _ := NewLibopusDecoder(48000, 2)
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	libDec.Destroy()

	// Compute per-sample stats
	t.Log("\nFirst 30 samples L/R comparison:")
	for i := 0; i < 30 && i*2+1 < len(goSamplesF32); i++ {
		libL := float64(libPcm[i*2])
		libR := float64(libPcm[i*2+1])
		goL := float64(goSamplesF32[i*2])
		goR := float64(goSamplesF32[i*2+1])

		// Compute M/S from L/R
		libM := (libL + libR) / 2
		libS := (libL - libR) / 2
		goM := (goL + goR) / 2
		goS := (goL - goR) / 2

		mDiff := goM - libM
		sDiff := goS - libS
		lDiff := goL - libL
		rDiff := goR - libR

		t.Logf("[%2d] M_diff=%+.3e S_diff=%+.3e | L_diff=%+.3e R_diff=%+.3e | lib_L=%.6f lib_R=%.6f",
			i, mDiff, sDiff, lDiff, rDiff, libL, libR)
	}

	// Overall stats
	var mSigPow, mNoisePow float64
	var sSigPow, sNoisePow float64

	for i := 0; i < libSamples && i*2+1 < len(goSamplesF32); i++ {
		libL := float64(libPcm[i*2])
		libR := float64(libPcm[i*2+1])
		goL := float64(goSamplesF32[i*2])
		goR := float64(goSamplesF32[i*2+1])

		libM := (libL + libR) / 2
		libS := (libL - libR) / 2
		goM := (goL + goR) / 2
		goS := (goL - goR) / 2

		mSigPow += libM * libM
		mNoisePow += (goM - libM) * (goM - libM)
		sSigPow += libS * libS
		sNoisePow += (goS - libS) * (goS - libS)
	}

	mSNR := 10 * math.Log10(mSigPow/mNoisePow)
	sSNR := 10 * math.Log10(sSigPow/sNoisePow)

	t.Logf("\nM channel SNR: %.1f dB", mSNR)
	t.Logf("S channel SNR: %.1f dB", sSNR)

	// The M and S should have similar SNR if they're decoded correctly
	// If they differ, one of them is being computed wrong
}

// TestAnalyzeErrorPattern examines the error pattern more deeply
func TestAnalyzeErrorPattern(t *testing.T) {
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

	// Analyze all problematic packets
	problematicPackets := []int{14, 15, 16, 17}
	for _, pktIdx := range problematicPackets {
		if pktIdx >= len(packets) {
			continue
		}

		pkt := packets[pktIdx]
		t.Logf("\n=== Packet %d (len=%d, TOC=0x%02X) ===", pktIdx, len(pkt), pkt[0])

		// Fresh decoders
		goDec, _ := gopus.NewDecoder(48000, 2)
		goSamplesF32, _ := goDec.DecodeFloat32(pkt)

		libDec, _ := NewLibopusDecoder(48000, 2)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		libDec.Destroy()

		// Analyze error characteristics
		var sumMErr, sumSErr float64
		var sumMErrAbs, sumSErrAbs float64
		var sumLErr, sumRErr float64
		count := 0

		for i := 0; i < libSamples && i*2+1 < len(goSamplesF32); i++ {
			libL := float64(libPcm[i*2])
			libR := float64(libPcm[i*2+1])
			goL := float64(goSamplesF32[i*2])
			goR := float64(goSamplesF32[i*2+1])

			libM := (libL + libR) / 2
			libS := (libL - libR) / 2
			goM := (goL + goR) / 2
			goS := (goL - goR) / 2

			mErr := goM - libM
			sErr := goS - libS

			sumMErr += mErr
			sumSErr += sErr
			sumMErrAbs += math.Abs(mErr)
			sumSErrAbs += math.Abs(sErr)
			sumLErr += goL - libL
			sumRErr += goR - libR
			count++
		}

		avgMErr := sumMErr / float64(count)
		avgSErr := sumSErr / float64(count)
		avgMErrAbs := sumMErrAbs / float64(count)
		avgSErrAbs := sumSErrAbs / float64(count)
		avgLErr := sumLErr / float64(count)
		avgRErr := sumRErr / float64(count)

		t.Logf("  Avg M error: %+.4e (abs: %.4e)", avgMErr, avgMErrAbs)
		t.Logf("  Avg S error: %+.4e (abs: %.4e)", avgSErr, avgSErrAbs)
		t.Logf("  Avg L error: %+.4e", avgLErr)
		t.Logf("  Avg R error: %+.4e", avgRErr)

		// Check if M error = S error (same sign same magnitude)
		if avgMErrAbs > 1e-10 && avgSErrAbs > 1e-10 {
			ratio := avgMErr / avgSErr
			t.Logf("  M/S error ratio: %.4f (if ~1.0, errors are identical)", ratio)
		}
	}
}

// TestComparePacketBytes compares raw bytes to identify difference source
func TestComparePacketBytes(t *testing.T) {
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

	// Analyze packet 14 structure
	pkt := packets[14]
	toc := pkt[0]
	config := toc >> 3
	stereo := (toc >> 2) & 1
	frameCount := toc & 3

	t.Logf("Packet 14 structure:")
	t.Logf("  TOC: 0x%02X", toc)
	t.Logf("  Config: %d (CELT-only)", config)
	t.Logf("  Stereo: %d", stereo)
	t.Logf("  Frame count code: %d", frameCount)
	t.Logf("  Raw bytes (first 40): %s", formatBytesHex(pkt[:minInt(40, len(pkt))]))

	// Decode the packet structure
	// For CELT, after TOC there may be padding bytes, then the range-coded data
	// The range coder starts from the end and works backwards
}

func formatBytesHex(b []byte) string {
	result := ""
	for i, v := range b {
		if i > 0 {
			result += " "
		}
		result += fmt.Sprintf("%02X", v)
	}
	return result
}
