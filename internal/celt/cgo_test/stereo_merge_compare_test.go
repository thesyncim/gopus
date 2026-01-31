// Package cgo compares stereo merge intermediate values
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

// TestStereoMergeCompare compares stereo merge between gopus and libopus
func TestStereoMergeCompare(t *testing.T) {
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

	// Enable stereo merge debug
	celt.DebugStereoMerge = true
	defer func() { celt.DebugStereoMerge = false }()

	// Decode packet 14 with fresh decoder
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))

	t.Log("=== Decoding packet 14 with stereo merge tracing ===")
	goSamples, _ := decodeFloat32(goDec, packets[14])

	// Compare with libopus
	libDec, _ := NewLibopusDecoder(48000, 2)
	if libDec == nil {
		t.Skip("Cannot create libopus decoder")
	}
	defer libDec.Destroy()

	libPcm, _ := libDec.DecodeFloat(packets[14], 5760)

	// Show first and last few samples
	t.Log("\nFirst 5 samples:")
	for i := 0; i < 5 && i*2+1 < len(goSamples); i++ {
		t.Logf("  [%d] L: go=%.6f lib=%.6f | R: go=%.6f lib=%.6f",
			i, goSamples[i*2], libPcm[i*2], goSamples[i*2+1], libPcm[i*2+1])
	}

	// Find max error location
	maxRErr := float64(0)
	maxRIdx := 0
	for i := 0; i < 240 && i*2+1 < len(goSamples); i++ {
		rErr := math.Abs(float64(goSamples[i*2+1]) - float64(libPcm[i*2+1]))
		if rErr > maxRErr {
			maxRErr = rErr
			maxRIdx = i
		}
	}

	t.Logf("\nMax R error at sample %d: %.6f", maxRIdx, maxRErr)
	t.Log("Samples around max error:")
	for i := maxRIdx - 3; i <= maxRIdx+3 && i >= 0 && i*2+1 < len(goSamples); i++ {
		t.Logf("  [%d] L: go=%.6f lib=%.6f diff=%.2e | R: go=%.6f lib=%.6f diff=%.2e",
			i, goSamples[i*2], libPcm[i*2], float64(goSamples[i*2])-float64(libPcm[i*2]),
			goSamples[i*2+1], libPcm[i*2+1], float64(goSamples[i*2+1])-float64(libPcm[i*2+1]))
	}
}

// TestOverlapDifference tests if the error is from overlap-add
func TestOverlapDifference(t *testing.T) {
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

	// Decode packet 14 TOC info
	pkt := packets[14]
	toc := pkt[0]
	config := toc >> 3
	stereo := (toc >> 2) & 1

	t.Logf("Packet 14: len=%d, config=%d (CELT NB 5ms), stereo=%d", len(pkt), config, stereo)

	// For CELT NB 5ms, frameSize = 240 samples at 48kHz
	// Overlap is typically 120 samples for short frames

	// Decode two fresh decoders
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	libDec, _ := NewLibopusDecoder(48000, 2)
	if libDec == nil {
		t.Skip("Cannot create libopus decoder")
	}
	defer libDec.Destroy()

	goSamples, _ := decodeFloat32(goDec, packets[14])
	libPcm, _ := libDec.DecodeFloat(packets[14], 5760)

	// The first overlap samples (0 to overlap-1) should show if overlap state matters
	// For fresh decoders, overlap buffer should be zero
	overlap := 120 // typical for 5ms CELT

	t.Log("\n=== First overlap region (0 to 120) ===")
	errSumOverlap := 0.0
	errSumAfter := 0.0

	for i := 0; i < 240 && i*2+1 < len(goSamples); i++ {
		rErr := math.Abs(float64(goSamples[i*2+1]) - float64(libPcm[i*2+1]))
		if i < overlap {
			errSumOverlap += rErr
		} else {
			errSumAfter += rErr
		}
	}

	t.Logf("Avg R error in overlap region (0-%d): %.6e", overlap, errSumOverlap/float64(overlap))
	t.Logf("Avg R error after overlap (%d-240): %.6e", overlap, errSumAfter/float64(240-overlap))
}
