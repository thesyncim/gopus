package celt

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestPacket31Debug(t *testing.T) {
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

	// Decode packets 29, 30, 31 with detailed tracing
	dec := NewDecoder(1)

	// Build up state with packets 0-28
	for i := 0; i < 29; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)
		dec.DecodeFrame(pkt[1:], frameSize)
	}

	// Now trace packets 29, 30, 31
	for i := 29; i <= 31; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)

		fmt.Printf("\n=== Packet %d ===\n", i)
		fmt.Printf("TOC: 0x%02x, cfg=%d, frameSize=%d\n", toc, cfg, frameSize)
		fmt.Printf("Packet bytes: %d\n", len(pkt))

		// Parse frame header
		rd := &rangecoding.Decoder{}
		rd.Init(pkt[1:])

		totalBits := len(pkt[1:]) * 8
		tell := rd.Tell()

		// Check for silence
		silence := false
		if tell >= totalBits {
			silence = true
		} else if tell == 1 {
			silence = rd.DecodeBit(15) == 1
		}
		fmt.Printf("Silence: %v\n", silence)

		if !silence {
			// Check postfilter
			postfilter := false
			postfilterPeriod := 0
			if tell+16 <= totalBits {
				if rd.DecodeBit(1) == 1 {
					postfilter = true
					octave := int(rd.DecodeUniform(6))
					postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
					rd.DecodeRawBits(3) // gain
					if rd.Tell()+2 <= totalBits {
						rd.DecodeICDF([]uint8{2, 1, 0}, 2) // tapset
					}
				}
			}
			fmt.Printf("Postfilter: %v, period=%d\n", postfilter, postfilterPeriod)

			mode := GetModeConfig(frameSize)
			lm := mode.LM
			tell = rd.Tell()
			transient := false
			if lm > 0 && tell+3 <= totalBits {
				transient = rd.DecodeBit(3) == 1
			}
			tell = rd.Tell()
			intra := false
			if tell+3 <= totalBits {
				intra = rd.DecodeBit(3) == 1
			}
			fmt.Printf("Transient: %v, Intra: %v\n", transient, intra)
		}

		// Get state before decode
		prevEBefore := make([]float64, 5)
		copy(prevEBefore, dec.PrevEnergy()[:5])
		overlapBefore := computeRMS(dec.OverlapBuffer())

		fmt.Printf("Before decode:\n")
		fmt.Printf("  PrevEnergy[0:5]: %.2f %.2f %.2f %.2f %.2f\n",
			prevEBefore[0], prevEBefore[1], prevEBefore[2], prevEBefore[3], prevEBefore[4])
		fmt.Printf("  Overlap RMS: %.4f\n", overlapBefore)

		// Decode
		samples, err := dec.DecodeFrame(pkt[1:], frameSize)
		if err != nil {
			fmt.Printf("Decode error: %v\n", err)
			continue
		}

		// Get state after decode
		prevEAfter := make([]float64, 5)
		copy(prevEAfter, dec.PrevEnergy()[:5])
		overlapAfter := computeRMS(dec.OverlapBuffer())

		fmt.Printf("After decode:\n")
		fmt.Printf("  PrevEnergy[0:5]: %.2f %.2f %.2f %.2f %.2f\n",
			prevEAfter[0], prevEAfter[1], prevEAfter[2], prevEAfter[3], prevEAfter[4])
		fmt.Printf("  Overlap RMS: %.4f\n", overlapAfter)

		// Sample statistics
		var sampleMax, sampleSum float64
		for _, s := range samples {
			if s > sampleMax {
				sampleMax = s
			} else if -s > sampleMax {
				sampleMax = -s
			}
			sampleSum += s * s
		}
		sampleRMS := sampleSum / float64(len(samples))
		fmt.Printf("  Sample max: %.4f, RMS: %.4f\n", sampleMax, sampleRMS)

		// Energy difference
		fmt.Printf("  Energy change: ")
		for j := 0; j < 5; j++ {
			diff := prevEAfter[j] - prevEBefore[j]
			fmt.Printf("%.2f ", diff)
		}
		fmt.Println()
	}
}
