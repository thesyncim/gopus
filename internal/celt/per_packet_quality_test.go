package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestPerPacketQuality(t *testing.T) {
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
	for offset+8 <= len(bitData) && len(packets) < 50 {
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

	for i, pkt := range packets {
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)

		samples, err := dec.DecodeFrame(pkt[1:], frameSize)
		if err != nil {
			fmt.Printf("Packet %2d: decode error: %v\n", i, err)
			continue
		}

		// Detect transient from packet
		isTransient := isPacketTransient(pkt)

		// Convert to int16 stereo
		stereoSamples := make([]int16, len(samples)*2)
		for j, s := range samples {
			val := int(s * 32768)
			if val > 32767 {
				val = 32767
			}
			if val < -32768 {
				val = -32768
			}
			stereoSamples[2*j] = int16(val)
			stereoSamples[2*j+1] = int16(val)
		}

		// Compute MSE against reference
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

		// Compute SNR and Q
		var signalPower float64
		for j := 0; j < len(stereoSamples) && samplePos+j < len(reference); j++ {
			ref := float64(reference[samplePos+j])
			signalPower += ref * ref
		}
		if count > 0 {
			signalPower /= float64(count)
		}

		snr := 10 * math.Log10(signalPower/mse)
		q := (snr - 48) * (100.0 / 48.0)

		transientStr := ""
		if isTransient {
			transientStr = " [TRANSIENT]"
		}

		fmt.Printf("Packet %2d: MSE=%8.2f, SNR=%6.2f dB, Q=%7.2f%s\n", i, mse, snr, q, transientStr)

		samplePos += len(samples) * 2
	}
}

func isPacketTransient(pkt []byte) bool {
	if len(pkt) < 2 {
		return false
	}
	toc := pkt[0]
	cfg := toc >> 3
	frameSize := getFrameSize(cfg)
	mode := GetModeConfig(frameSize)
	if mode.LM == 0 {
		return false
	}

	// Simple check: try to decode the transient bit
	// This is a simplified version - just checking bit position
	// In reality this requires range decoder
	return false // Simplified - we'll use the test that properly decodes
}
