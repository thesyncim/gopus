// Package cgo compares intensity decoding between gopus and libopus
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestIntensityEffect checks if the issue is related to intensity stereo transition
func TestIntensityEffect(t *testing.T) {
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

	// Decode all packets and track when error appears
	goDec, _ := gopus.NewDecoderDefault(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	t.Log("Tracking per-sample error growth:")
	t.Log("Pkt | Max L err | Max R err | First R err sample")
	t.Log("----|-----------|-----------|-------------------")

	for pktIdx := 0; pktIdx <= 20 && pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]

		goSamplesF32, _ := decodeFloat32(goDec, pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		maxLErr := 0.0
		maxRErr := 0.0
		firstRErrSample := -1

		for i := 0; i < libSamples && i*2+1 < len(goSamplesF32); i++ {
			libL := float64(libPcm[i*2])
			libR := float64(libPcm[i*2+1])
			goL := float64(goSamplesF32[i*2])
			goR := float64(goSamplesF32[i*2+1])

			lErr := math.Abs(goL - libL)
			rErr := math.Abs(goR - libR)

			if lErr > maxLErr {
				maxLErr = lErr
			}
			if rErr > maxRErr {
				maxRErr = rErr
			}

			if firstRErrSample == -1 && rErr > 1e-6 {
				firstRErrSample = i
			}
		}

		t.Logf("%3d | %.3e   | %.3e   | %d", pktIdx, maxLErr, maxRErr, firstRErrSample)
	}
}

// TestDualStereoDecoding tests dual stereo decoding specifically
func TestDualStereoDecoding(t *testing.T) {
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

	// Focus on packet 14 where the error first appears
	pkt := packets[14]
	t.Logf("Packet 14: len=%d", len(pkt))

	// Decode with fresh decoders
	freshGo, _ := gopus.NewDecoderDefault(48000, 2)
	freshLib, _ := NewLibopusDecoder(48000, 2)
	defer freshLib.Destroy()

	goSamples, _ := decodeFloat32(freshGo, pkt)
	libPcm, libSamples := freshLib.DecodeFloat(pkt, 5760)

	// Find where the error is largest
	maxRErr := 0.0
	maxRIdx := 0
	for i := 0; i < libSamples && i*2+1 < len(goSamples); i++ {
		rErr := math.Abs(float64(goSamples[i*2+1]) - float64(libPcm[i*2+1]))
		if rErr > maxRErr {
			maxRErr = rErr
			maxRIdx = i
		}
	}

	t.Logf("Max R error at sample %d: %.6f", maxRIdx, maxRErr)

	// Show samples around the max error
	t.Log("Samples around max error:")
	for i := maxRIdx - 5; i <= maxRIdx+5 && i >= 0 && i < libSamples && i*2+1 < len(goSamples); i++ {
		libL := libPcm[i*2]
		libR := libPcm[i*2+1]
		goL := goSamples[i*2]
		goR := goSamples[i*2+1]
		t.Logf("  [%d] L: lib=%.6f go=%.6f diff=%.2e | R: lib=%.6f go=%.6f diff=%.2e",
			i, libL, goL, goL-libL, libR, goR, goR-libR)
	}
}
