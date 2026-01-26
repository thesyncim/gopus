package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestCoeffDebug(t *testing.T) {
	testDir := filepath.Join("..", "testvectors", "testdata", "opus_testvectors")
	bitFile := filepath.Join(testDir, "testvector07.bit")

	bitData, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Test data not available: %v", err)
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

	// Instrument decoder to capture intermediate values
	dec := NewDecoder(1)

	// Build up state with packets 0-30
	for i := 0; i < 31; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)
		dec.DecodeFrame(pkt[1:], frameSize)
	}

	// Now decode packet 31 with detailed coefficient tracing
	pkt := packets[31]
	toc := pkt[0]
	cfg := toc >> 3
	frameSize := getFrameSize(cfg)
	bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
	dec.SetBandwidth(bw)

	fmt.Printf("=== Packet 31 Analysis ===\n")
	fmt.Printf("Frame size: %d, bandwidth: %v\n", frameSize, bw)

	// Get overlap before
	overlapBefore := make([]float64, len(dec.OverlapBuffer()))
	copy(overlapBefore, dec.OverlapBuffer())

	// Decode
	samples, _ := dec.DecodeFrame(pkt[1:], frameSize)

	// Get overlap after
	overlapAfter := dec.OverlapBuffer()

	fmt.Printf("\nOverlap buffer comparison:\n")
	fmt.Printf("Position  | Before    | After      | Diff\n")
	fmt.Printf("----------|-----------|------------|--------\n")
	maxDiff := 0.0
	maxDiffPos := 0
	for i := 0; i < 120 && i < len(overlapBefore) && i < len(overlapAfter); i++ {
		diff := math.Abs(overlapAfter[i] - overlapBefore[i])
		if diff > maxDiff {
			maxDiff = diff
			maxDiffPos = i
		}
		if i < 10 || (i >= 55 && i <= 65) || i >= 110 {
			fmt.Printf("%4d      | %9.2f | %10.2f | %8.2f\n",
				i, overlapBefore[i], overlapAfter[i], overlapAfter[i]-overlapBefore[i])
		}
	}
	fmt.Printf("Max diff: %.2f at position %d\n", maxDiff, maxDiffPos)

	// Check sample statistics
	fmt.Printf("\nSample analysis (first 20 and last 20):\n")
	fmt.Printf("Position | Sample\n")
	for i := 0; i < 20 && i < len(samples); i++ {
		fmt.Printf("%4d     | %10.6f\n", i, samples[i])
	}
	fmt.Println("...")
	for i := len(samples) - 20; i < len(samples); i++ {
		if i >= 0 {
			fmt.Printf("%4d     | %10.6f\n", i, samples[i])
		}
	}

	// Compute energy per region
	var sumFirst, sumMiddle, sumLast float64
	for i := 0; i < 120 && i < len(samples); i++ {
		sumFirst += samples[i] * samples[i]
	}
	for i := 120; i < 840 && i < len(samples); i++ {
		sumMiddle += samples[i] * samples[i]
	}
	for i := 840; i < len(samples); i++ {
		sumLast += samples[i] * samples[i]
	}
	fmt.Printf("\nEnergy by region:\n")
	fmt.Printf("  First 120:  RMS=%.6f\n", math.Sqrt(sumFirst/120))
	fmt.Printf("  Middle 720: RMS=%.6f\n", math.Sqrt(sumMiddle/720))
	fmt.Printf("  Last 120:   RMS=%.6f\n", math.Sqrt(sumLast/120))
}
