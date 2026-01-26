package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestPacket31Detailed analyzes packet 31 decoding in detail.
func TestPacket31Detailed(t *testing.T) {
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

	// Decode packets 0-31 and analyze packet 31 in detail
	dec := NewDecoder(1)
	samplePos := 0

	for i := 0; i <= 31; i++ {
		pkt := packets[i]
		if len(pkt) == 0 {
			continue
		}
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)

		samples, err := dec.DecodeFrame(pkt[1:], frameSize)
		if err != nil {
			t.Fatalf("Packet %d decode error: %v", i, err)
		}

		// For packet 31, do detailed analysis
		if i == 31 {
			fmt.Printf("\n=== PACKET 31 DETAILED ANALYSIS ===\n")
			fmt.Printf("Packet length: %d bytes\n", len(pkt))
			fmt.Printf("Frame size: %d\n", frameSize)
			fmt.Printf("Decoded samples: %d\n", len(samples))
			fmt.Printf("Reference position: %d\n", samplePos)

			// Convert to int16 for comparison
			stereoSamples := make([]int16, len(samples)*2)
			for j, s := range samples {
				val := int16(s * 32768)
				stereoSamples[2*j] = val
				stereoSamples[2*j+1] = val
			}

			// Find first divergence point
			divergeIdx := -1
			for j := 0; j < len(stereoSamples) && samplePos+j < len(reference); j++ {
				diff := int(stereoSamples[j]) - int(reference[samplePos+j])
				if math.Abs(float64(diff)) > 5 {
					divergeIdx = j
					break
				}
			}

			if divergeIdx >= 0 {
				fmt.Printf("\nFirst significant divergence at sample %d (mono idx %d):\n", divergeIdx, divergeIdx/2)

				// Show context around divergence
				start := divergeIdx - 10
				if start < 0 {
					start = 0
				}
				end := divergeIdx + 20
				if end > len(stereoSamples) {
					end = len(stereoSamples)
				}

				fmt.Printf("Samples around divergence:\n")
				for j := start; j < end && samplePos+j < len(reference); j++ {
					diff := int(stereoSamples[j]) - int(reference[samplePos+j])
					marker := ""
					if j == divergeIdx {
						marker = " <-- FIRST"
					}
					fmt.Printf("  [%4d] ref=%6d got=%6d diff=%6d%s\n",
						j, reference[samplePos+j], stereoSamples[j], diff, marker)
				}

				// Calculate MSE for different regions
				mse1 := 0.0
				for j := 0; j < divergeIdx && samplePos+j < len(reference); j++ {
					diff := float64(stereoSamples[j]) - float64(reference[samplePos+j])
					mse1 += diff * diff
				}
				if divergeIdx > 0 {
					mse1 /= float64(divergeIdx)
				}

				mse2 := 0.0
				count2 := 0
				for j := divergeIdx; j < len(stereoSamples) && samplePos+j < len(reference); j++ {
					diff := float64(stereoSamples[j]) - float64(reference[samplePos+j])
					mse2 += diff * diff
					count2++
				}
				if count2 > 0 {
					mse2 /= float64(count2)
				}

				fmt.Printf("\nMSE before divergence: %.2f\n", mse1)
				fmt.Printf("MSE after divergence: %.2f\n", mse2)

			} else {
				fmt.Printf("No significant divergence found!\n")

				// Show sample comparison anyway
				fmt.Printf("\nFirst 20 samples:\n")
				for j := 0; j < 20 && j < len(stereoSamples) && samplePos+j < len(reference); j++ {
					diff := int(stereoSamples[j]) - int(reference[samplePos+j])
					fmt.Printf("  [%4d] ref=%6d got=%6d diff=%6d\n",
						j, reference[samplePos+j], stereoSamples[j], diff)
				}
			}

			// Show overlap buffer state
			fmt.Printf("\nOverlap buffer RMS: %.4f\n", computeRMS(dec.OverlapBuffer()))
		}

		samplePos += len(samples) * 2
	}
}

// TestPacket30vs31 compares decoding of packets 30 and 31 to find the difference.
func TestPacket30vs31(t *testing.T) {
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

	// Analyze packets 30 and 31 raw bytes
	for i := 30; i <= 31; i++ {
		pkt := packets[i]
		fmt.Printf("\n=== PACKET %d RAW ANALYSIS ===\n", i)
		fmt.Printf("Length: %d bytes\n", len(pkt))

		// Print first 8 bytes in binary to see flags
		fmt.Printf("First 8 bytes (hex): ")
		for j := 0; j < 8 && j < len(pkt); j++ {
			fmt.Printf("%02x ", pkt[j])
		}
		fmt.Printf("\n")

		// Analyze TOC byte
		toc := pkt[0]
		cfg := toc >> 3
		stereo := (toc & 0x4) != 0
		frameCode := toc & 0x3
		fmt.Printf("TOC: 0x%02X (cfg=%d, stereo=%v, frameCode=%d)\n", toc, cfg, stereo, frameCode)

		// Analyze the second byte (first byte of range coder payload)
		if len(pkt) > 1 {
			b1 := pkt[1]
			// Silence flag is the first bit
			// Postfilter flag follows
			// Transient flag follows (for LM > 0)
			// Intra flag follows
			fmt.Printf("Byte 1: 0x%02X (binary: %08b)\n", b1, b1)
		}
	}
}
