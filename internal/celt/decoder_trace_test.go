package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestDecoderCoefficientsTrace(t *testing.T) {
	testDir := filepath.Join("..", "testvectors", "testdata", "opus_testvectors")
	bitFile := filepath.Join(testDir, "testvector07.bit")

	bitData, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Test data not available: %v", err)
	}

	// Parse first packet
	packetLen := binary.BigEndian.Uint32(bitData[0:])
	pkt := bitData[8 : 8+int(packetLen)]

	toc := pkt[0]
	cfg := toc >> 3
	frameSize := getFrameSize(cfg)

	fmt.Printf("Packet: %d bytes, cfg=%d, frameSize=%d\n", len(pkt), cfg, frameSize)
	fmt.Printf("Packet bytes: %02x\n", pkt)

	// Initialize range decoder
	rd := &rangecoding.Decoder{}
	rd.Init(pkt[1:])

	totalBits := len(pkt[1:]) * 8
	tell := rd.Tell()
	fmt.Printf("Total bits: %d, initial tell: %d\n", totalBits, tell)

	// Check silence
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	fmt.Printf("Silence: %v\n", silence)

	if !silence {
		// Postfilter
		postfilter := false
		if tell+16 <= totalBits {
			postfilter = rd.DecodeBit(1) == 1
		}
		fmt.Printf("Postfilter: %v\n", postfilter)
		if postfilter {
			octave := int(rd.DecodeUniform(6))
			period := (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			gain := int(rd.DecodeRawBits(3))
			fmt.Printf("  Octave=%d, Period=%d, Gain=%d\n", octave, period, gain)
			if rd.Tell()+2 <= totalBits {
				tapset := int(rd.DecodeICDF([]uint8{2, 1, 0}, 2))
				fmt.Printf("  Tapset=%d\n", tapset)
			}
		}

		// LM and transient
		mode := GetModeConfig(frameSize)
		lm := mode.LM
		tell = rd.Tell()
		transient := false
		if lm > 0 && tell+3 <= totalBits {
			transient = rd.DecodeBit(3) == 1
		}
		fmt.Printf("LM=%d, Transient=%v\n", lm, transient)

		// Intra
		tell = rd.Tell()
		intra := false
		if tell+3 <= totalBits {
			intra = rd.DecodeBit(3) == 1
		}
		fmt.Printf("Intra=%v\n", intra)

		// Decode the frame using the decoder
		dec := NewDecoder(1)
		dec.SetBandwidth(BandwidthFromOpusConfig(int(getBandwidthType(cfg))))
		samples, _ := dec.DecodeFrame(pkt[1:], frameSize)

		// Print decoded sample statistics
		var minS, maxS float64
		for _, s := range samples {
			if s < minS {
				minS = s
			}
			if s > maxS {
				maxS = s
			}
		}
		fmt.Printf("\nDecoded samples: min=%.6f, max=%.6f\n", minS, maxS)

		// Print overlap buffer
		overlap := dec.OverlapBuffer()
		var minO, maxO float64
		for _, o := range overlap {
			if o < minO {
				minO = o
			}
			if o > maxO {
				maxO = o
			}
		}
		fmt.Printf("Overlap buffer: min=%.6f, max=%.6f\n", minO, maxO)

		// Print first few samples
		fmt.Printf("\nFirst 10 decoded samples:\n")
		for i := 0; i < 10 && i < len(samples); i++ {
			fmt.Printf("  [%d] = %.9f\n", i, samples[i])
		}

		// Print prevEnergy state
		prevE := dec.PrevEnergy()
		fmt.Printf("\nPrevEnergy (first 10 bands):\n")
		for i := 0; i < 10 && i < len(prevE); i++ {
			fmt.Printf("  Band %d: %.4f\n", i, prevE[i])
		}
	}
}

func TestBandEnergyDecode(t *testing.T) {
	testDir := filepath.Join("..", "testvectors", "testdata", "opus_testvectors")
	bitFile := filepath.Join(testDir, "testvector07.bit")

	bitData, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Test data not available: %v", err)
	}

	// Parse first few packets
	var packets [][]byte
	offset := 0
	for offset+8 <= len(bitData) && len(packets) < 5 {
		packetLen := binary.BigEndian.Uint32(bitData[offset:])
		offset += 8
		if offset+int(packetLen) > len(bitData) {
			break
		}
		packets = append(packets, bitData[offset:offset+int(packetLen)])
		offset += int(packetLen)
	}

	dec := NewDecoder(1)

	for i, pkt := range packets {
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)

		// Get state before
		prevEBefore := make([]float64, 10)
		copy(prevEBefore, dec.PrevEnergy()[:10])

		samples, _ := dec.DecodeFrame(pkt[1:], frameSize)

		// Get state after
		prevEAfter := dec.PrevEnergy()

		// Sample stats
		var rms float64
		for _, s := range samples {
			rms += s * s
		}
		rms = math.Sqrt(rms / float64(len(samples)))

		fmt.Printf("Packet %d: frameSize=%d, samples=%d, RMS=%.6f\n", i, frameSize, len(samples), rms)
		fmt.Printf("  Energy change (first 5 bands):\n")
		for b := 0; b < 5; b++ {
			fmt.Printf("    Band %d: %.4f -> %.4f (delta=%.4f)\n",
				b, prevEBefore[b], prevEAfter[b], prevEAfter[b]-prevEBefore[b])
		}
	}
}
