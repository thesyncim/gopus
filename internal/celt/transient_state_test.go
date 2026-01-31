package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestTransientStateCorruption(t *testing.T) {
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

	// Test 1: Decode packets 0-17 (before first transient)
	fmt.Println("=== Test 1: Decode packets 0-17 (before transient) ===")
	dec1 := NewDecoder(1)
	samplePos1 := 0
	for i := 0; i <= 17; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec1.SetBandwidth(bw)
		samples, _ := dec1.DecodeFrame(pkt[1:], frameSize)
		samplePos1 += len(samples) * 2
	}
	overlap17 := make([]float64, len(dec1.OverlapBuffer()))
	copy(overlap17, dec1.OverlapBuffer())
	prevE17 := make([]float64, len(dec1.PrevEnergy()))
	copy(prevE17, dec1.PrevEnergy())

	fmt.Printf("After packet 17 (before transient):\n")
	fmt.Printf("  Overlap RMS: %.4f\n", computeRMS(overlap17))
	fmt.Printf("  PrevEnergy[0:5]: ")
	for i := 0; i < 5 && i < len(prevE17); i++ {
		fmt.Printf("%.2f ", prevE17[i])
	}
	fmt.Println()

	// Test 2: Decode packet 18 (first transient)
	fmt.Println("\n=== Test 2: Decode packet 18 (first transient) ===")
	{
		pkt := packets[18]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec1.SetBandwidth(bw)
		samples, _ := dec1.DecodeFrame(pkt[1:], frameSize)

		// Compute MSE
		stereoSamples := make([]int16, len(samples)*2)
		for j, s := range samples {
			val := int16(s * 32768)
			stereoSamples[2*j] = val
			stereoSamples[2*j+1] = val
		}
		var mse float64
		for j := 0; j < len(stereoSamples) && samplePos1+j < len(reference); j++ {
			diff := float64(stereoSamples[j]) - float64(reference[samplePos1+j])
			mse += diff * diff
		}
		mse /= float64(len(stereoSamples))

		fmt.Printf("Packet 18 (transient): MSE=%.2f\n", mse)
		fmt.Printf("  Overlap RMS: %.4f\n", computeRMS(dec1.OverlapBuffer()))
		samplePos1 += len(samples) * 2
	}

	// Test 3: Continue decoding and track state
	fmt.Println("\n=== Test 3: Track state through packets 19-35 ===")
	for i := 19; i <= 35; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec1.SetBandwidth(bw)

		overlapBefore := computeRMS(dec1.OverlapBuffer())
		samples, _ := dec1.DecodeFrame(pkt[1:], frameSize)
		overlapAfter := computeRMS(dec1.OverlapBuffer())

		stereoSamples := make([]int16, len(samples)*2)
		for j, s := range samples {
			val := int16(s * 32768)
			stereoSamples[2*j] = val
			stereoSamples[2*j+1] = val
		}
		var mse float64
		for j := 0; j < len(stereoSamples) && samplePos1+j < len(reference); j++ {
			diff := float64(stereoSamples[j]) - float64(reference[samplePos1+j])
			mse += diff * diff
		}
		mse /= float64(len(stereoSamples))

		fmt.Printf("Packet %d: MSE=%10.2f, overlap RMS: %.4f -> %.4f\n",
			i, mse, overlapBefore, overlapAfter)
		samplePos1 += len(samples) * 2
	}

	// Test 4: Fresh decoder skipping transients
	fmt.Println("\n=== Test 4: Fresh decoder, skip transient packets 18-20 ===")
	dec2 := NewDecoder(1)
	samplePos2 := 0
	for i := 0; i <= 35; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec2.SetBandwidth(bw)

		// Skip packets 18-20 (transient packets)
		if i >= 18 && i <= 20 {
			samplePos2 += frameSize * 2
			continue
		}

		samples, _ := dec2.DecodeFrame(pkt[1:], frameSize)

		stereoSamples := make([]int16, len(samples)*2)
		for j, s := range samples {
			val := int16(s * 32768)
			stereoSamples[2*j] = val
			stereoSamples[2*j+1] = val
		}
		var mse float64
		count := 0
		for j := 0; j < len(stereoSamples) && samplePos2+j < len(reference); j++ {
			diff := float64(stereoSamples[j]) - float64(reference[samplePos2+j])
			mse += diff * diff
			count++
		}
		if count > 0 {
			mse /= float64(count)
		}

		if i >= 28 && i <= 35 {
			fmt.Printf("Packet %d: MSE=%10.2f (skipped 18-20)\n", i, mse)
		}
		samplePos2 += len(samples) * 2
	}
}

func computeRMS(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	var sum float64
	for _, v := range data {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(data)))
}
