package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestPacketByPacketAnalysis(t *testing.T) {
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

	// Parse reference samples (stereo interleaved)
	reference := make([]int16, len(decData)/2)
	for i := range reference {
		reference[i] = int16(binary.LittleEndian.Uint16(decData[i*2:]))
	}

	// Parse packets
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

	fmt.Printf("Analyzing %d packets, %d reference samples\n\n", len(packets), len(reference))

	// Track when MSE first exceeds threshold
	firstBadPacket := -1
	prevFrameSize := 0

	for i := 0; i < len(packets) && i < 50; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)

		samples, err := dec.DecodeFrame(pkt[1:], frameSize)
		if err != nil {
			fmt.Printf("Packet %d: decode error: %v\n", i, err)
			continue
		}

		// Convert to stereo int16 for comparison
		stereoSamples := make([]int16, len(samples)*2)
		for j, s := range samples {
			val := int16(s * 32768)
			stereoSamples[2*j] = val
			stereoSamples[2*j+1] = val
		}

		// Compute MSE
		var sumSqErr float64
		count := 0
		for j := 0; j < len(stereoSamples) && samplePos+j < len(reference); j++ {
			diff := float64(stereoSamples[j]) - float64(reference[samplePos+j])
			sumSqErr += diff * diff
			count++
		}
		var mse float64
		if count > 0 {
			mse = sumSqErr / float64(count)
		}

		frameSizeChange := ""
		if prevFrameSize != 0 && prevFrameSize != frameSize {
			frameSizeChange = fmt.Sprintf(" (was %d)", prevFrameSize)
		}

		if mse > 100 {
			if firstBadPacket < 0 {
				firstBadPacket = i
			}
			fmt.Printf("Packet %d: frameSize=%d%s, MSE=%.2f **BAD**\n", i, frameSize, frameSizeChange, mse)
		} else if i >= firstBadPacket-2 && firstBadPacket > 0 {
			fmt.Printf("Packet %d: frameSize=%d%s, MSE=%.2f\n", i, frameSize, frameSizeChange, mse)
		} else if i < 5 || (i >= 25 && i <= 35) {
			fmt.Printf("Packet %d: frameSize=%d%s, MSE=%.2f\n", i, frameSize, frameSizeChange, mse)
		}

		prevFrameSize = frameSize
		samplePos += len(samples) * 2
	}

	if firstBadPacket >= 0 {
		fmt.Printf("\n=== First bad packet: %d ===\n", firstBadPacket)
	}
}

func getFrameSize(cfg byte) int {
	switch {
	case cfg <= 3:
		return []int{480, 960, 1920, 2880}[cfg]
	case cfg <= 7:
		return []int{480, 960, 1920, 2880}[cfg-4]
	case cfg <= 11:
		return []int{480, 960, 1920, 2880}[cfg-8]
	case cfg <= 13:
		return []int{480, 960}[cfg-12]
	case cfg <= 15:
		return []int{480, 960}[cfg-14]
	case cfg <= 19:
		return []int{120, 240, 480, 960}[cfg-16]
	case cfg <= 23:
		return []int{120, 240, 480, 960}[cfg-20]
	case cfg <= 27:
		return []int{120, 240, 480, 960}[cfg-24]
	case cfg <= 31:
		return []int{120, 240, 480, 960}[cfg-28]
	default:
		return 960
	}
}

func getBandwidthType(cfg byte) int {
	switch {
	case cfg <= 3:
		return 0
	case cfg <= 7:
		return 1
	case cfg <= 11:
		return 2
	case cfg <= 13:
		return 3
	case cfg <= 15:
		return 4
	case cfg <= 19:
		return 0
	case cfg <= 23:
		return 1
	case cfg <= 27:
		return 2
	case cfg <= 31:
		return 4
	default:
		return 4
	}
}

func TestCompareTestVectors(t *testing.T) {
	testDir := filepath.Join("..", "testvectors", "testdata", "opus_testvectors")

	// Compare testvector01 (passes) with testvector07 (fails)
	for _, vecName := range []string{"testvector01", "testvector07", "testvector11"} {
		bitFile := filepath.Join(testDir, vecName+".bit")
		decFile := filepath.Join(testDir, vecName+".dec")

		bitData, err := os.ReadFile(bitFile)
		if err != nil {
			t.Logf("%s: skipped (no bit file)", vecName)
			continue
		}
		decData, err := os.ReadFile(decFile)
		if err != nil {
			t.Logf("%s: skipped (no dec file)", vecName)
			continue
		}

		// Parse packets
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

		// Count frame sizes
		frameSizes := make(map[int]int)
		for _, pkt := range packets {
			toc := pkt[0]
			cfg := toc >> 3
			fs := getFrameSize(cfg)
			frameSizes[fs]++
		}

		// Determine stereo from first packet
		stereo := (packets[0][0] & 0x04) != 0
		channels := 1
		if stereo {
			channels = 2
		}

		// Decode first 50 packets and compute average MSE
		dec := NewDecoder(channels)
		reference := make([]int16, len(decData)/2)
		for i := range reference {
			reference[i] = int16(binary.LittleEndian.Uint16(decData[i*2:]))
		}

		var totalMSE float64
		samplePos := 0
		badCount := 0
		for i := 0; i < len(packets) && i < 50; i++ {
			pkt := packets[i]
			toc := pkt[0]
			cfg := toc >> 3
			frameSize := getFrameSize(cfg)
			bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
			dec.SetBandwidth(bw)

			samples, err := dec.DecodeFrame(pkt[1:], frameSize)
			if err != nil {
				continue
			}

			// Convert to int16 for comparison
			var sumSqErr float64
			count := 0
			for j, s := range samples {
				val := int16(s * 32768)
				for c := 0; c < channels; c++ {
					refIdx := samplePos + j*channels + c
					if refIdx < len(reference) {
						diff := float64(val) - float64(reference[refIdx])
						sumSqErr += diff * diff
						count++
					}
				}
			}
			var mse float64
			if count > 0 {
				mse = sumSqErr / float64(count)
			}
			totalMSE += mse
			if mse > 100 {
				badCount++
			}
			samplePos += len(samples) * channels
		}

		avgMSE := totalMSE / 50
		snr := 10 * math.Log10(32768*32768/avgMSE)
		q := (snr - 48) * (100 / 48)

		fmt.Printf("%s: %d packets, channels=%d, frameSizes=%v, avgMSE=%.2f, SNR=%.2f dB, Q=%.2f, badPackets=%d/50\n",
			vecName, len(packets), channels, frameSizes, avgMSE, snr, q, badCount)
	}
}
