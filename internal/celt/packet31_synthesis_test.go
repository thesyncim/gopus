package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestPacket31Synthesis(t *testing.T) {
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
	for offset+8 <= len(bitData) {
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

	// Decode all packets to position 30
	for i := 0; i < 30; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)
		samples, _ := dec.DecodeFrame(pkt[1:], frameSize)
		samplePos += len(samples) * 2 // stereo output
	}

	fmt.Printf("Starting detailed analysis at sample position %d\n", samplePos)

	// Save overlap state before packet 30
	overlapBefore30 := make([]float64, len(dec.OverlapBuffer()))
	copy(overlapBefore30, dec.OverlapBuffer())

	// Decode packet 30
	pkt30 := packets[30]
	toc := pkt30[0]
	cfg := toc >> 3
	frameSize := getFrameSize(cfg)
	bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
	dec.SetBandwidth(bw)

	fmt.Printf("\n=== PACKET 30 ===\n")
	fmt.Printf("Frame size: %d\n", frameSize)

	samples30, err := dec.DecodeFrame(pkt30[1:], frameSize)
	if err != nil {
		t.Fatalf("Packet 30 decode error: %v", err)
	}

	// Get reference samples for packet 30
	refStart30 := samplePos
	refEnd30 := refStart30 + len(samples30)*2
	if refEnd30 > len(reference) {
		refEnd30 = len(reference)
	}
	fmt.Printf("Reference range: %d - %d\n", refStart30, refEnd30)

	// Compare first 20 samples
	fmt.Printf("Packet 30 first 20 samples vs reference:\n")
	for j := 0; j < 20 && j < len(samples30); j++ {
		val := int(samples30[j] * 32768)
		if val > 32767 {
			val = 32767
		}
		if val < -32768 {
			val = -32768
		}
		refIdx := samplePos + j*2 // stereo interleaved
		refVal := int(0)
		if refIdx < len(reference) {
			refVal = int(reference[refIdx])
		}
		diff := val - refVal
		fmt.Printf("  [%2d] decoded=%6d, ref=%6d, diff=%6d\n", j, val, refVal, diff)
	}

	// Overlap buffer after packet 30
	overlapAfter30 := make([]float64, len(dec.OverlapBuffer()))
	copy(overlapAfter30, dec.OverlapBuffer())

	fmt.Printf("\nOverlap buffer after packet 30:\n")
	fmt.Printf("  First 10: ")
	for j := 0; j < 10; j++ {
		fmt.Printf("%.2f ", overlapAfter30[j])
	}
	fmt.Printf("\n")
	fmt.Printf("  Last 10 ([110:120]): ")
	for j := 110; j < 120; j++ {
		fmt.Printf("%.2f ", overlapAfter30[j])
	}
	fmt.Printf("\n")
	fmt.Printf("  Overlap RMS: %.4f\n", computeRMS(overlapAfter30))

	// Compute MSE for packet 30
	var mse30 float64
	count30 := 0
	for j := 0; j < len(samples30)*2 && samplePos+j < len(reference); j++ {
		stereoIdx := j / 2
		if stereoIdx < len(samples30) {
			val := int(samples30[stereoIdx] * 32768)
			if val > 32767 {
				val = 32767
			}
			if val < -32768 {
				val = -32768
			}
			diff := float64(val) - float64(reference[samplePos+j])
			mse30 += diff * diff
			count30++
		}
	}
	if count30 > 0 {
		mse30 /= float64(count30)
	}
	fmt.Printf("Packet 30 MSE: %.2f\n", mse30)

	samplePos += len(samples30) * 2

	// Now decode packet 31
	pkt31 := packets[31]
	toc = pkt31[0]
	cfg = toc >> 3
	frameSize = getFrameSize(cfg)
	bw = BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
	dec.SetBandwidth(bw)

	fmt.Printf("\n=== PACKET 31 ===\n")
	fmt.Printf("Frame size: %d\n", frameSize)

	samples31, err := dec.DecodeFrame(pkt31[1:], frameSize)
	if err != nil {
		t.Fatalf("Packet 31 decode error: %v", err)
	}

	// Get reference samples for packet 31
	refStart31 := samplePos
	refEnd31 := refStart31 + len(samples31)*2
	if refEnd31 > len(reference) {
		refEnd31 = len(reference)
	}
	fmt.Printf("Reference range: %d - %d\n", refStart31, refEnd31)

	// Compare first 20 samples
	fmt.Printf("Packet 31 first 20 samples vs reference:\n")
	for j := 0; j < 20 && j < len(samples31); j++ {
		val := int(samples31[j] * 32768)
		if val > 32767 {
			val = 32767
		}
		if val < -32768 {
			val = -32768
		}
		refIdx := samplePos + j*2 // stereo interleaved
		refVal := int(0)
		if refIdx < len(reference) {
			refVal = int(reference[refIdx])
		}
		diff := val - refVal
		fmt.Printf("  [%2d] decoded=%6d, ref=%6d, diff=%6d\n", j, val, refVal, diff)
	}

	// Overlap buffer after packet 31
	overlapAfter31 := make([]float64, len(dec.OverlapBuffer()))
	copy(overlapAfter31, dec.OverlapBuffer())

	fmt.Printf("\nOverlap buffer after packet 31:\n")
	fmt.Printf("  First 10: ")
	for j := 0; j < 10; j++ {
		fmt.Printf("%.2f ", overlapAfter31[j])
	}
	fmt.Printf("\n")
	fmt.Printf("  Last 10 ([110:120]): ")
	for j := 110; j < 120; j++ {
		fmt.Printf("%.2f ", overlapAfter31[j])
	}
	fmt.Printf("\n")
	fmt.Printf("  Overlap RMS: %.4f\n", computeRMS(overlapAfter31))

	// Compute MSE for packet 31
	var mse31 float64
	count31 := 0
	for j := 0; j < len(samples31)*2 && samplePos+j < len(reference); j++ {
		stereoIdx := j / 2
		if stereoIdx < len(samples31) {
			val := int(samples31[stereoIdx] * 32768)
			if val > 32767 {
				val = 32767
			}
			if val < -32768 {
				val = -32768
			}
			diff := float64(val) - float64(reference[samplePos+j])
			mse31 += diff * diff
			count31++
		}
	}
	if count31 > 0 {
		mse31 /= float64(count31)
	}
	fmt.Printf("Packet 31 MSE: %.2f\n", mse31)

	// Look at specific sample regions
	fmt.Printf("\nSample magnitude analysis:\n")
	var sum30, sum31 float64
	for _, s := range samples30 {
		sum30 += math.Abs(s)
	}
	for _, s := range samples31 {
		sum31 += math.Abs(s)
	}
	fmt.Printf("  Packet 30 avg abs: %.6f\n", sum30/float64(len(samples30)))
	fmt.Printf("  Packet 31 avg abs: %.6f\n", sum31/float64(len(samples31)))

	// Check overlap difference
	fmt.Printf("\nOverlap change analysis:\n")
	var overlapDiff float64
	for j := range overlapAfter30 {
		d := overlapAfter31[j] - overlapAfter30[j]
		overlapDiff += d * d
	}
	fmt.Printf("  Overlap buffer MSE change: %.4f\n", math.Sqrt(overlapDiff/float64(len(overlapAfter30))))
}
