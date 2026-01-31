package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestFirstPacketTrace(t *testing.T) {
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

	// Parse reference samples (stereo interleaved for mono -> L=R)
	reference := make([]int16, len(decData)/2)
	for i := range reference {
		reference[i] = int16(binary.LittleEndian.Uint16(decData[i*2:]))
	}

	// Parse first packet
	packetLen := binary.BigEndian.Uint32(bitData[0:])
	_ = binary.BigEndian.Uint32(bitData[4:]) // skip
	pkt := bitData[8 : 8+int(packetLen)]

	toc := pkt[0]
	cfg := toc >> 3
	frameSize := getFrameSize(cfg)
	bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))

	fmt.Printf("First packet: %d bytes, cfg=%d, frameSize=%d, bw=%v\n", len(pkt), cfg, frameSize, bw)

	// Decode
	dec := NewDecoder(1)
	dec.SetBandwidth(bw)
	samples, err := dec.DecodeFrame(pkt[1:], frameSize)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	fmt.Printf("Decoded %d samples\n", len(samples))

	// Convert to int16
	decoded := make([]int16, len(samples))
	for i, s := range samples {
		decoded[i] = int16(s * 32768)
	}

	// Compare first 20 samples
	fmt.Printf("\nFirst 20 samples comparison:\n")
	fmt.Printf("Pos | Decoded | Reference L | Reference R | Diff L | Diff R\n")
	for i := 0; i < 20 && 2*i+1 < len(reference); i++ {
		refL := reference[2*i]
		refR := reference[2*i+1]
		dec := decoded[i]
		diffL := int(dec) - int(refL)
		diffR := int(dec) - int(refR)
		fmt.Printf("%3d | %7d | %7d | %7d | %7d | %7d\n", i, dec, refL, refR, diffL, diffR)
	}

	// Compute MSE for different regions
	var sumFirst, sumMiddle, sumLast float64
	for i := 0; i < 120 && i < len(decoded); i++ {
		refL := float64(reference[2*i])
		diff := float64(decoded[i]) - refL
		sumFirst += diff * diff
	}
	for i := 120; i < 840 && i < len(decoded); i++ {
		refL := float64(reference[2*i])
		diff := float64(decoded[i]) - refL
		sumMiddle += diff * diff
	}
	for i := 840; i < len(decoded); i++ {
		refL := float64(reference[2*i])
		diff := float64(decoded[i]) - refL
		sumLast += diff * diff
	}

	mseFirst := sumFirst / 120
	mseMiddle := sumMiddle / 720
	mseLast := sumLast / 120

	fmt.Printf("\nMSE by region:\n")
	fmt.Printf("  First 120 (overlap): MSE=%.2f\n", mseFirst)
	fmt.Printf("  Middle 720: MSE=%.2f\n", mseMiddle)
	fmt.Printf("  Last 120: MSE=%.2f\n", mseLast)

	// Check sample statistics
	var maxDec, maxRef int16
	for i := 0; i < len(decoded) && 2*i < len(reference); i++ {
		if decoded[i] > maxDec {
			maxDec = decoded[i]
		}
		if -decoded[i] > maxDec {
			maxDec = -decoded[i]
		}
		refL := reference[2*i]
		if refL > maxRef {
			maxRef = refL
		}
		if -refL > maxRef {
			maxRef = -refL
		}
	}
	fmt.Printf("\nMax amplitude: decoded=%d, reference=%d\n", maxDec, maxRef)

	// Check if samples have correct sign pattern
	fmt.Printf("\nSign comparison (first 10):\n")
	for i := 0; i < 10 && 2*i < len(reference); i++ {
		decSign := "+"
		if decoded[i] < 0 {
			decSign = "-"
		} else if decoded[i] == 0 {
			decSign = "0"
		}
		refSign := "+"
		if reference[2*i] < 0 {
			refSign = "-"
		} else if reference[2*i] == 0 {
			refSign = "0"
		}
		match := ""
		if decSign != refSign {
			match = " MISMATCH"
		}
		fmt.Printf("%d: dec=%s ref=%s%s\n", i, decSign, refSign, match)
	}
}

func TestIMDCTNormalization(t *testing.T) {
	// Test that IMDCT produces values in expected range
	N := 480
	spectrum := make([]float64, N)

	// Test with unit impulse at DC
	spectrum[0] = 1.0
	out := IMDCTDirect(spectrum)
	fmt.Printf("DC impulse response (first 10 of %d):\n", len(out))
	for i := 0; i < 10; i++ {
		fmt.Printf("  [%d] = %.6f\n", i, out[i])
	}

	// Find max
	var maxVal float64
	for _, v := range out {
		if math.Abs(v) > maxVal {
			maxVal = math.Abs(v)
		}
	}
	fmt.Printf("Max value: %.6f\n", maxVal)

	// Test with all ones
	for i := range spectrum {
		spectrum[i] = 1.0
	}
	out = IMDCTDirect(spectrum)
	fmt.Printf("\nAll-ones response (first 10):\n")
	for i := 0; i < 10; i++ {
		fmt.Printf("  [%d] = %.6f\n", i, out[i])
	}

	// Sum of all outputs
	var sum float64
	for _, v := range out {
		sum += v
	}
	fmt.Printf("Sum of outputs: %.6f\n", sum)
}
