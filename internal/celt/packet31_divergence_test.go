package celt

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPacket31Divergence(t *testing.T) {
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
	for i := 0; i < 31; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)
		samples, _ := dec.DecodeFrame(pkt[1:], frameSize)
		samplePos += len(samples) * 2 // stereo output
	}

	// Now decode packet 31
	pkt31 := packets[31]
	toc := pkt31[0]
	cfg := toc >> 3
	frameSize := getFrameSize(cfg)
	bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
	dec.SetBandwidth(bw)

	samples31, err := dec.DecodeFrame(pkt31[1:], frameSize)
	if err != nil {
		t.Fatalf("Packet 31 decode error: %v", err)
	}

	// Find where divergence starts
	fmt.Printf("Packet 31 has %d samples (mono)\n", len(samples31))
	fmt.Printf("Reference position: %d to %d\n", samplePos, samplePos+len(samples31)*2)

	// Check samples at different positions
	positions := []int{0, 50, 100, 150, 200, 250, 300, 350, 400, 450, 500, 550, 600, 650, 700, 750, 800, 850, 900, 950}
	fmt.Printf("\nSample comparison at key positions (mono sample index):\n")
	fmt.Printf("%6s %8s %8s %8s\n", "idx", "decoded", "ref", "diff")

	firstBigDiff := -1
	for _, idx := range positions {
		if idx < len(samples31) {
			val := int(samples31[idx] * 32768)
			if val > 32767 {
				val = 32767
			}
			if val < -32768 {
				val = -32768
			}
			refIdx := samplePos + idx*2 // stereo interleaved
			refVal := int(0)
			if refIdx < len(reference) {
				refVal = int(reference[refIdx])
			}
			diff := val - refVal
			absDiff := diff
			if absDiff < 0 {
				absDiff = -absDiff
			}
			marker := ""
			if absDiff > 100 && firstBigDiff == -1 {
				firstBigDiff = idx
				marker = " <-- DIVERGENCE"
			}
			if absDiff > 100 {
				marker = " ***"
			}
			fmt.Printf("%6d %8d %8d %8d%s\n", idx, val, refVal, diff, marker)
		}
	}

	if firstBigDiff != -1 {
		// Zoom in around the divergence point
		fmt.Printf("\nZoomed in around sample %d:\n", firstBigDiff)
		start := firstBigDiff - 20
		if start < 0 {
			start = 0
		}
		for idx := start; idx < firstBigDiff+30 && idx < len(samples31); idx++ {
			val := int(samples31[idx] * 32768)
			if val > 32767 {
				val = 32767
			}
			if val < -32768 {
				val = -32768
			}
			refIdx := samplePos + idx*2 // stereo interleaved
			refVal := int(0)
			if refIdx < len(reference) {
				refVal = int(reference[refIdx])
			}
			diff := val - refVal
			fmt.Printf("%6d %8d %8d %8d\n", idx, val, refVal, diff)
		}
	}

	// Look at last 100 samples
	fmt.Printf("\nLast 50 samples:\n")
	for idx := len(samples31) - 50; idx < len(samples31); idx++ {
		val := int(samples31[idx] * 32768)
		if val > 32767 {
			val = 32767
		}
		if val < -32768 {
			val = -32768
		}
		refIdx := samplePos + idx*2 // stereo interleaved
		refVal := int(0)
		if refIdx < len(reference) {
			refVal = int(reference[refIdx])
		}
		diff := val - refVal
		fmt.Printf("%6d %8d %8d %8d\n", idx, val, refVal, diff)
	}
}
