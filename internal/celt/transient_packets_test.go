package celt

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestAnalyzeTransientPackets analyzes testvector07 packets to identify transient frames.
func TestAnalyzeTransientPackets(t *testing.T) {
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

	fmt.Printf("Total packets: %d\n\n", len(packets))

	// Analyze each packet's TOC and check for transient flag
	fmt.Printf("%-5s %-4s %-6s %-10s %-10s %-8s %-10s %-10s\n",
		"Pkt", "Len", "TOC", "Cfg", "FrameSize", "Mode", "Bandwidth", "Notes")
	fmt.Println("--------------------------------------------------------------------------------")

	for i, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		mode := getBandwidthType(cfg)

		modeStr := ""
		switch {
		case cfg <= 3:
			modeStr = "NB/SILK"
		case cfg <= 7:
			modeStr = "MB/SILK"
		case cfg <= 11:
			modeStr = "WB/SILK"
		case cfg <= 13:
			modeStr = "SWB/Hybrid"
		case cfg <= 15:
			modeStr = "FB/Hybrid"
		case cfg <= 19:
			modeStr = "NB/CELT"
		case cfg <= 23:
			modeStr = "WB/CELT"
		case cfg <= 27:
			modeStr = "SWB/CELT"
		default:
			modeStr = "FB/CELT"
		}

		bwStr := ""
		switch mode {
		case 0:
			bwStr = "NB"
		case 1:
			bwStr = "MB"
		case 2:
			bwStr = "WB"
		case 3:
			bwStr = "SWB"
		case 4:
			bwStr = "FB"
		}

		// Check frame code for channel count
		frameCode := toc & 0x3
		stereo := (toc & 0x4) != 0

		notes := ""
		if stereo {
			notes = "stereo"
		} else {
			notes = "mono"
		}
		if frameCode > 0 {
			notes += fmt.Sprintf(" frames=%d", frameCode+1)
		}

		// For CELT frames, try to detect transient
		if cfg >= 16 && len(pkt) > 1 {
			mode := GetModeConfig(frameSize)
			if mode.LM > 0 {
				// Transient flag is encoded early in the frame
				// We can't easily decode it without the full range decoder,
				// but we can note that transients are possible
				notes += " (transient-possible)"
			}
		}

		fmt.Printf("%-5d %-4d 0x%02X   %-10d %-10d %-8s %-10s %s\n",
			i, len(pkt), toc, cfg, frameSize, modeStr, bwStr, notes)
	}

	// Focus on packets 18-35 (the problematic range from transient test)
	fmt.Printf("\n\n=== Detailed analysis of packets 18-35 ===\n")
	for i := 18; i <= 35 && i < len(packets); i++ {
		pkt := packets[i]
		if len(pkt) == 0 {
			continue
		}
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		stereo := (toc & 0x4) != 0

		fmt.Printf("Packet %d: len=%d, TOC=0x%02X, cfg=%d, frameSize=%d, stereo=%v\n",
			i, len(pkt), toc, cfg, frameSize, stereo)

		// Print first 16 bytes (hex)
		hexStr := ""
		for j := 0; j < 16 && j < len(pkt); j++ {
			hexStr += fmt.Sprintf("%02x", pkt[j])
		}
		fmt.Printf("  First 16 bytes: %s\n", hexStr)
	}
}

// TestDecodeTransientPackets decodes the transient packets and checks results.
func TestDecodeTransientPackets(t *testing.T) {
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

	// Decode all packets with verbose logging for 28-35
	dec := NewDecoder(1)
	samplePos := 0

	fmt.Printf("\n=== Decoding all packets, verbose for 28-35 ===\n")
	for i, pkt := range packets {
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
			t.Logf("Packet %d: decode error: %v", i, err)
			continue
		}

		// Calculate MSE
		stereoSamples := make([]int16, len(samples)*2)
		for j, s := range samples {
			val := int16(s * 32768)
			stereoSamples[2*j] = val
			stereoSamples[2*j+1] = val
		}

		var mse float64
		count := 0
		for j := 0; j < len(stereoSamples) && samplePos+j < len(reference); j++ {
			diff := float64(stereoSamples[j]) - float64(reference[samplePos+j])
			mse += diff * diff
			count++
		}
		if count > 0 {
			mse /= float64(count)
		}

		if i >= 28 && i <= 35 {
			fmt.Printf("Packet %d: frameSize=%d, MSE=%.2f, overlap RMS=%.4f\n",
				i, frameSize, mse, computeRMS(dec.OverlapBuffer()))

			// Sample comparison
			if count >= 4 {
				fmt.Printf("  First 4 samples: ref=[%d,%d,%d,%d] got=[%d,%d,%d,%d]\n",
					reference[samplePos], reference[samplePos+1], reference[samplePos+2], reference[samplePos+3],
					stereoSamples[0], stereoSamples[1], stereoSamples[2], stereoSamples[3])
			}
		}

		samplePos += len(samples) * 2
	}
}
