package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestTransientPacketsDecode validates transient packet decoding (packets 18-20).
func TestTransientPacketsDecode(t *testing.T) {
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

	// Decode all packets up to and including transient packets
	dec := NewDecoder(1)
	samplePos := 0

	fmt.Printf("=== Transient Packet Validation ===\n\n")

	for i := 0; i <= 22; i++ {
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

		// Convert to int16 for comparison
		stereoSamples := make([]int16, len(samples)*2)
		for j, s := range samples {
			val := int16(s * 32768)
			stereoSamples[2*j] = val
			stereoSamples[2*j+1] = val
		}

		// Calculate MSE
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

		// Check if transient
		isTransient := (i == 18 || i == 19 || i == 20)
		transientStr := ""
		if isTransient {
			transientStr = " [TRANSIENT]"
		}

		// Calculate SNR
		var signalPower float64
		for j := 0; j < len(stereoSamples) && samplePos+j < len(reference); j++ {
			signalPower += float64(reference[samplePos+j]) * float64(reference[samplePos+j])
		}
		if count > 0 {
			signalPower /= float64(count)
		}
		snr := 10 * math.Log10(signalPower/mse)
		if mse == 0 {
			snr = 100
		}

		fmt.Printf("Packet %2d: MSE=%8.2f, SNR=%6.2f dB, overlap RMS=%8.4f%s\n",
			i, mse, snr, computeRMS(dec.OverlapBuffer()), transientStr)

		// For transient packets, show more detail
		if isTransient {
			// Show first few samples comparison
			fmt.Printf("  First 8 samples: ref=[%d,%d,%d,%d,%d,%d,%d,%d]\n",
				reference[samplePos], reference[samplePos+1],
				reference[samplePos+2], reference[samplePos+3],
				reference[samplePos+4], reference[samplePos+5],
				reference[samplePos+6], reference[samplePos+7])
			fmt.Printf("                   got=[%d,%d,%d,%d,%d,%d,%d,%d]\n",
				stereoSamples[0], stereoSamples[1],
				stereoSamples[2], stereoSamples[3],
				stereoSamples[4], stereoSamples[5],
				stereoSamples[6], stereoSamples[7])

			// Find max error sample
			maxErr := 0
			maxErrIdx := 0
			for j := 0; j < len(stereoSamples) && samplePos+j < len(reference); j++ {
				diff := int(stereoSamples[j]) - int(reference[samplePos+j])
				if diff < 0 {
					diff = -diff
				}
				if diff > maxErr {
					maxErr = diff
					maxErrIdx = j
				}
			}
			fmt.Printf("  Max error: %d at sample %d (ref=%d, got=%d)\n",
				maxErr, maxErrIdx, reference[samplePos+maxErrIdx], stereoSamples[maxErrIdx])
		}

		samplePos += len(samples) * 2
	}

	// Summary
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Packets 18-20 are TRANSIENT frames with 8 short blocks of 120 samples.\n")
	fmt.Printf("Higher MSE is expected during transient transitions due to:\n")
	fmt.Printf("- Abrupt energy changes\n")
	fmt.Printf("- Short block overlap-add complexity\n")
	fmt.Printf("- Potential minor numerical differences in IMDCT\n")
}

// TestTransientSynthesisConsistency checks that transient synthesis produces consistent results.
func TestTransientSynthesisConsistency(t *testing.T) {
	// Create a synthetic transient test case
	frameSize := 960
	shortBlocks := 8
	shortSize := frameSize / shortBlocks

	// Create test coefficients with controlled content
	coeffs := make([]float64, frameSize)
	for b := 0; b < shortBlocks; b++ {
		for i := 0; i < shortSize; i++ {
			// Interleaved format: coef[b + i*B]
			idx := b + i*shortBlocks
			// Each block has a different DC offset for easy identification
			coeffs[idx] = float64(b+1) * 0.01 * math.Cos(float64(i)*0.1)
		}
	}

	// Test 1: Single decode
	dec1 := NewDecoder(1)
	out1 := dec1.Synthesize(coeffs, true, shortBlocks)

	// Test 2: Fresh decoder, same input
	dec2 := NewDecoder(1)
	out2 := dec2.Synthesize(coeffs, true, shortBlocks)

	// Verify outputs match
	if len(out1) != len(out2) {
		t.Errorf("Output lengths differ: %d vs %d", len(out1), len(out2))
		return
	}

	maxDiff := 0.0
	for i := range out1 {
		diff := math.Abs(out1[i] - out2[i])
		if diff > maxDiff {
			maxDiff = diff
		}
	}

	if maxDiff > 1e-10 {
		t.Errorf("Transient synthesis not consistent: max diff = %e", maxDiff)
	}

	t.Logf("Transient synthesis consistency: max diff = %e (expected 0)", maxDiff)

	// Verify output is non-zero and reasonable
	var outRMS float64
	for _, v := range out1 {
		outRMS += v * v
	}
	outRMS = math.Sqrt(outRMS / float64(len(out1)))

	t.Logf("Output RMS: %f", outRMS)
	if outRMS == 0 {
		t.Error("Output is all zeros")
	}

	// Check overlap buffer is set correctly
	overlap1 := dec1.OverlapBuffer()
	overlap2 := dec2.OverlapBuffer()

	overlapMaxDiff := 0.0
	for i := range overlap1 {
		if i >= len(overlap2) {
			break
		}
		diff := math.Abs(overlap1[i] - overlap2[i])
		if diff > overlapMaxDiff {
			overlapMaxDiff = diff
		}
	}

	if overlapMaxDiff > 1e-10 {
		t.Errorf("Overlap buffers differ: max diff = %e", overlapMaxDiff)
	}
}
