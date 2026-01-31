package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestPacket5to6Transition(t *testing.T) {
	testDir := filepath.Join("..", "testvectors", "testdata", "opus_testvectors")
	bitFile := filepath.Join(testDir, "testvector07.bit")
	decFile := filepath.Join(testDir, "testvector07.dec")

	bitData, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Test data not available: %v", err)
	}
	decData, err := os.ReadFile(decFile)
	if err != nil {
		t.Skipf("Reference not available: %v", err)
	}

	reference := make([]int16, len(decData)/2)
	for i := range reference {
		reference[i] = int16(binary.LittleEndian.Uint16(decData[i*2:]))
	}

	var packets [][]byte
	offset := 0
	for offset+8 <= len(bitData) && len(packets) < 10 {
		packetLen := binary.BigEndian.Uint32(bitData[offset:])
		offset += 8
		if offset+int(packetLen) > len(bitData) {
			break
		}
		packets = append(packets, bitData[offset:offset+int(packetLen)])
		offset += int(packetLen)
	}

	dec := NewDecoder(1)
	samplePos := 0

	for i := 0; i < 8; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)

		// Get state before
		overlapRMSBefore := computeRMS(dec.OverlapBuffer())
		prevEBefore := make([]float64, 5)
		copy(prevEBefore, dec.PrevEnergy()[:5])

		samples, err := dec.DecodeFrame(pkt[1:], frameSize)
		if err != nil {
			fmt.Printf("Packet %d: decode error: %v\n", i, err)
			continue
		}

		// Get state after
		overlapRMSAfter := computeRMS(dec.OverlapBuffer())
		prevEAfter := dec.PrevEnergy()

		// Compute MSE
		stereoSamples := make([]int16, len(samples)*2)
		for j, s := range samples {
			val := int16(s * 32768)
			stereoSamples[2*j] = val
			stereoSamples[2*j+1] = val
		}
		var mse float64
		for j := 0; j < len(stereoSamples) && samplePos+j < len(reference); j++ {
			diff := float64(stereoSamples[j]) - float64(reference[samplePos+j])
			mse += diff * diff
		}
		mse /= float64(len(stereoSamples))

		// Sample statistics
		var sMin, sMax, sRMS float64
		for _, s := range samples {
			if s < sMin {
				sMin = s
			}
			if s > sMax {
				sMax = s
			}
			sRMS += s * s
		}
		sRMS = math.Sqrt(sRMS / float64(len(samples)))

		fmt.Printf("\n=== Packet %d ===\n", i)
		fmt.Printf("MSE: %.2f\n", mse)
		fmt.Printf("Overlap RMS: %.6f -> %.6f\n", overlapRMSBefore, overlapRMSAfter)
		fmt.Printf("PrevEnergy[0:5]: %.2f,%.2f,%.2f,%.2f,%.2f -> %.2f,%.2f,%.2f,%.2f,%.2f\n",
			prevEBefore[0], prevEBefore[1], prevEBefore[2], prevEBefore[3], prevEBefore[4],
			prevEAfter[0], prevEAfter[1], prevEAfter[2], prevEAfter[3], prevEAfter[4])
		fmt.Printf("Sample range: [%.6f, %.6f], RMS=%.6f\n", sMin, sMax, sRMS)

		// Detailed look at overlap buffer
		overlap := dec.OverlapBuffer()
		fmt.Printf("Overlap first 10: ")
		for j := 0; j < 10 && j < len(overlap); j++ {
			fmt.Printf("%.4f ", overlap[j])
		}
		fmt.Println()
		fmt.Printf("Overlap last 10: ")
		for j := len(overlap) - 10; j < len(overlap); j++ {
			if j >= 0 {
				fmt.Printf("%.4f ", overlap[j])
			}
		}
		fmt.Println()

		samplePos += len(samples) * 2
	}
}
