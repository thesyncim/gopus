//go:build cgo_libopus
// +build cgo_libopus

// drift_accumulation_test.go - Test how overlap buffer drift accumulates over frames
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestDriftAccumulationRate measures how fast overlap/state drift accumulates
func TestDriftAccumulationRate(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
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

	t.Log("Testing overlap buffer drift by decoding packet 61 after varying history lengths:")
	t.Log("History | SNR (dB) | State Err | Output Diff Sum")
	t.Log("--------+----------+-----------+----------------")

	historyLengths := []int{0, 10, 20, 30, 40, 50, 55, 58, 60}

	for _, histLen := range historyLengths {
		// Create fresh decoders
		goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
		libDec, _ := NewLibopusDecoder(48000, 2)
		defer libDec.Destroy()

		// Decode history packets
		for i := 0; i < histLen && i < 61; i++ {
			decodeFloat32(goDec, packets[i])
			libDec.DecodeFloat(packets[i], 5760)
		}

		// Decode packet 61
		goPcm, _ := decodeFloat32(goDec, packets[61])
		libPcm, libN := libDec.DecodeFloat(packets[61], 5760)

		// Compute metrics
		n := minInt(len(goPcm), libN*2)
		var sig, noise, sumAbsDiff float64
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
			sumAbsDiff += math.Abs(d)
		}
		snr := 10 * math.Log10(sig/noise)

		// Get state error
		libMem0, _ := libDec.GetPreemphState()
		goState := goDec.GetCELTDecoder().PreemphState()
		stateErr := math.Abs(goState[0] - float64(libMem0))

		t.Logf("  %5d  |  %6.1f  |  %.6f | %.6f",
			histLen, snr, stateErr, sumAbsDiff)
	}
}

// TestOverlapBufferDriftPerFrame traces overlap buffer drift frame by frame
func TestOverlapBufferDriftPerFrame(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
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

	// Decode ALL packets with both decoders and track output difference
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	t.Log("Per-frame output difference accumulation:")
	t.Log("Pkt | Frame | SNR (dB) | Cum.Diff | State Err")
	t.Log("----+-------+----------+----------+----------")

	var cumulativeDiff float64

	for i := 0; i < 70 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, _ := decodeFloat32(goDec, pkt)
		libPcm, libN := libDec.DecodeFloat(pkt, 5760)

		n := minInt(len(goPcm), libN*2)
		var sig, noise, frameDiff float64
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
			frameDiff += math.Abs(d)
		}
		snr := 10 * math.Log10(sig/noise)
		cumulativeDiff += frameDiff

		libMem0, _ := libDec.GetPreemphState()
		goState := goDec.GetCELTDecoder().PreemphState()
		stateErr := math.Abs(goState[0] - float64(libMem0))

		// Only log every 10th frame or frames of interest
		if i%10 == 0 || (i >= 55 && i <= 65) || stateErr > 0.001 {
			t.Logf(" %2d | %5d | %7.1f  | %.4f   | %.6f",
				i, toc.FrameSize, snr, cumulativeDiff, stateErr)
		}
	}
}

// TestCompareOverlapEnergy compares overlap buffer energy between gopus decoding
// the same packets starting from different points
func TestCompareOverlapEnergy(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
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

	// Decoder 1: decode packets 0-60
	goDec1, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	for i := 0; i <= 60; i++ {
		decodeFloat32(goDec1, packets[i])
	}

	// Decoder 2: decode only packets 55-60 (less history)
	goDec2, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	for i := 55; i <= 60; i++ {
		decodeFloat32(goDec2, packets[i])
	}

	// Compare overlap buffers
	overlap1 := goDec1.GetCELTDecoder().OverlapBuffer()
	overlap2 := goDec2.GetCELTDecoder().OverlapBuffer()

	var energy1, energy2, diffEnergy float64
	for i := 0; i < len(overlap1) && i < len(overlap2); i++ {
		energy1 += overlap1[i] * overlap1[i]
		energy2 += overlap2[i] * overlap2[i]
		d := overlap1[i] - overlap2[i]
		diffEnergy += d * d
	}

	t.Log("Overlap buffer comparison after decoding to packet 60:")
	t.Logf("  Decoder with 0-60 history: energy = %.2f", energy1)
	t.Logf("  Decoder with 55-60 history: energy = %.2f", energy2)
	t.Logf("  Difference energy: %.6f", diffEnergy)

	// Note: The overlap buffers should be different because they have different history
	// But this tells us how much the history affects the overlap buffer
	if diffEnergy > 0.001 {
		t.Log("  -> Different histories produce different overlap buffers (expected)")
	}
}
