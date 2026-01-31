package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestFullDecodeTrace(t *testing.T) {
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

	// Parse reference
	reference := make([]int16, len(decData)/2)
	for i := range reference {
		reference[i] = int16(binary.LittleEndian.Uint16(decData[i*2:]))
	}

	// Parse first packet
	packetLen := binary.BigEndian.Uint32(bitData[0:])
	pkt := bitData[8 : 8+int(packetLen)]

	toc := pkt[0]
	cfg := toc >> 3
	frameSize := getFrameSize(cfg)
	bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))

	fmt.Printf("Packet: %d bytes, frameSize=%d\n", len(pkt), frameSize)

	// Full decode
	dec := NewDecoder(1)
	dec.SetBandwidth(bw)
	samples, err := dec.DecodeFrame(pkt[1:], frameSize)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	fmt.Printf("Decoded samples: %d\n", len(samples))

	// Sample statistics
	var minS, maxS, rms float64
	for _, s := range samples {
		if s < minS {
			minS = s
		}
		if s > maxS {
			maxS = s
		}
		rms += s * s
	}
	rms = math.Sqrt(rms / float64(len(samples)))

	fmt.Printf("Decoded: min=%.9f, max=%.9f, RMS=%.9f\n", minS, maxS, rms)

	// Convert to int16
	decoded := make([]int16, len(samples))
	for i, s := range samples {
		v := int(s * 32768)
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		decoded[i] = int16(v)
	}

	// Reference statistics
	var refMin, refMax int16
	var refRMS float64
	for i := 0; i < len(decoded)*2 && i < len(reference); i += 2 {
		r := reference[i]
		if r < refMin {
			refMin = r
		}
		if r > refMax {
			refMax = r
		}
		refRMS += float64(r) * float64(r)
	}
	refRMS = math.Sqrt(refRMS / float64(len(decoded)))

	fmt.Printf("Reference: min=%d, max=%d, RMS=%.2f\n", refMin, refMax, refRMS)

	// Decoded int16 statistics
	var decMin, decMax int16
	var decRMS float64
	for _, d := range decoded {
		if d < decMin {
			decMin = d
		}
		if d > decMax {
			decMax = d
		}
		decRMS += float64(d) * float64(d)
	}
	decRMS = math.Sqrt(decRMS / float64(len(decoded)))

	fmt.Printf("Decoded int16: min=%d, max=%d, RMS=%.2f\n", decMin, decMax, decRMS)

	// Sample comparison
	fmt.Printf("\nFirst 20 samples:\n")
	fmt.Printf("Pos | Decoded | RefL | Diff\n")
	for i := 0; i < 20 && i < len(decoded) && 2*i < len(reference); i++ {
		fmt.Printf("%3d | %7d | %5d | %5d\n", i, decoded[i], reference[2*i], int(decoded[i])-int(reference[2*i]))
	}

	// Check middle samples
	fmt.Printf("\nMiddle samples [460:480]:\n")
	for i := 460; i < 480 && i < len(decoded) && 2*i < len(reference); i++ {
		fmt.Printf("%3d | %7d | %5d | %5d\n", i, decoded[i], reference[2*i], int(decoded[i])-int(reference[2*i]))
	}

	// Check last samples
	fmt.Printf("\nLast samples [%d:%d]:\n", len(decoded)-20, len(decoded))
	for i := len(decoded) - 20; i < len(decoded) && 2*i < len(reference); i++ {
		if i >= 0 {
			fmt.Printf("%3d | %7d | %5d | %5d\n", i, decoded[i], reference[2*i], int(decoded[i])-int(reference[2*i]))
		}
	}
}
