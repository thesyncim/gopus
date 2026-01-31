// Package cgo tests intensity stereo behavior
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestIntensityStereoPacket14 checks if packet 14 is actually intensity stereo
func TestIntensityStereoPacket14(t *testing.T) {
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

	pkt := packets[14]
	t.Logf("Packet 14: len=%d, TOC=0x%02X", len(pkt), pkt[0])

	// Decode with libopus
	libDec, _ := NewLibopusDecoder(48000, 2)
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	libDec.Destroy()

	// Check if L and R are identical (which would indicate intensity stereo)
	var lrDiffSum, lrDiffSq float64
	for i := 0; i < libSamples; i++ {
		l := float64(libPcm[i*2])
		r := float64(libPcm[i*2+1])
		diff := l - r
		lrDiffSum += diff
		lrDiffSq += diff * diff
	}

	rmsDiff := math.Sqrt(lrDiffSq / float64(libSamples))
	t.Logf("Libopus L-R: avg=%.6f, rms=%.6f", lrDiffSum/float64(libSamples), rmsDiff)

	// Check the actual L and R values
	t.Log("First 10 libopus samples:")
	for i := 0; i < 10 && i < libSamples; i++ {
		l := libPcm[i*2]
		r := libPcm[i*2+1]
		t.Logf("  [%d] L=%.6f, R=%.6f, diff=%.6f", i, l, r, l-r)
	}

	// Now decode with gopus
	goDec, _ := gopus.NewDecoderDefault(48000, 2)
	goSamples, _ := goDec.DecodeFloat32(pkt)

	t.Log("First 10 gopus samples:")
	for i := 0; i < 10 && i*2+1 < len(goSamples); i++ {
		l := goSamples[i*2]
		r := goSamples[i*2+1]
		t.Logf("  [%d] L=%.6f, R=%.6f, diff=%.6f", i, l, r, l-r)
	}

	// Check gopus L-R
	var goLRDiffSum, goLRDiffSq float64
	for i := 0; i*2+1 < len(goSamples); i++ {
		l := float64(goSamples[i*2])
		r := float64(goSamples[i*2+1])
		diff := l - r
		goLRDiffSum += diff
		goLRDiffSq += diff * diff
	}
	goRmsDiff := math.Sqrt(goLRDiffSq / float64(len(goSamples)/2))
	t.Logf("Gopus L-R: avg=%.6f, rms=%.6f", goLRDiffSum/float64(len(goSamples)/2), goRmsDiff)

	// If both are near-zero, it's true intensity stereo
	if rmsDiff < 1e-6 && goRmsDiff < 1e-6 {
		t.Log("Both decoders produce identical L and R - true intensity stereo")
	} else {
		t.Logf("L-R differs: libopus rms=%.6f, gopus rms=%.6f", rmsDiff, goRmsDiff)
	}
}
