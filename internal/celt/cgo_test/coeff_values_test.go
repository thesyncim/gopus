// coeff_values_test.go - Check coefficient values for each short block
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestShortBlockCoeffMagnitudes checks the coefficient magnitudes for each short block
func TestShortBlockCoeffMagnitudes(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	// Decode packet 61 to get coefficient values
	pkt61 := packets[61]
	toc := gopus.ParseTOC(pkt61[0])
	if toc.Mode != gopus.ModeCELT {
		t.Skip("Packet 61 is not CELT mode")
	}

	celtData := pkt61[1:]
	rd := &rangecoding.Decoder{}
	rd.Init(celtData)

	mode := celt.GetModeConfig(toc.FrameSize)

	// Skip header parsing (silence, postfilter, transient flags)
	_ = rd.DecodeBit(15)      // silence
	if rd.DecodeBit(1) == 1 { // postfilter
		octave := int(rd.DecodeUniform(6))
		_ = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
		_ = rd.DecodeRawBits(3)
		if rd.Tell()+2 <= len(celtData)*8 {
			_ = rd.DecodeRawBits(2)
		}
	}
	transient := false
	if mode.LM > 0 && rd.Tell()+3 <= len(celtData)*8 {
		transient = rd.DecodeBit(3) == 1
	}

	t.Logf("Packet 61: transient=%v, frameSize=%d", transient, toc.FrameSize)

	// We can't easily get the decoded coefficients without replicating the full decode
	// Instead, let's analyze the OUTPUT values to infer coefficient characteristics

	// Build up decoder state first
	goDec, _ := gopus.NewDecoder(48000, 2)
	for i := 0; i < 61; i++ {
		goDec.DecodeFloat32(packets[i])
	}
	goPcm, _ := goDec.DecodeFloat32(pkt61)

	// Analyze the output values per block
	t.Log("\nOutput value statistics per short block:")
	t.Log("Block | Min      | Max      | RMS      | Abs Max  | Zero crossings")
	t.Log("------+----------+----------+----------+----------+---------------")

	shortBlocks := 8
	shortSize := toc.FrameSize / shortBlocks

	for b := 0; b < shortBlocks; b++ {
		startSample := b * shortSize
		endSample := (b + 1) * shortSize

		var sum, sumSq, minVal, maxVal float64
		minVal = math.MaxFloat64
		maxVal = -math.MaxFloat64
		zeroCrossings := 0
		prevSign := 0.0

		for i := startSample; i < endSample; i++ {
			idxL := i * 2
			if idxL < len(goPcm) {
				v := float64(goPcm[idxL])
				sum += v
				sumSq += v * v
				if v < minVal {
					minVal = v
				}
				if v > maxVal {
					maxVal = v
				}
				// Count zero crossings
				if prevSign != 0 && v*prevSign < 0 {
					zeroCrossings++
				}
				if v != 0 {
					prevSign = math.Copysign(1, v)
				}
			}
		}

		rms := math.Sqrt(sumSq / float64(shortSize))
		absMax := math.Max(math.Abs(minVal), math.Abs(maxVal))

		t.Logf("  %d   | %8.5f | %8.5f | %8.5f | %8.5f | %d",
			b, minVal, maxVal, rms, absMax, zeroCrossings)
	}

	// Check for DC offset or unusual patterns
	t.Log("\nChecking for unusual patterns in blocks 4-6:")
	for b := 4; b <= 6; b++ {
		startSample := b * shortSize
		var sumL, sumR float64
		for i := startSample; i < startSample+shortSize; i++ {
			idxL := i * 2
			idxR := i*2 + 1
			if idxL < len(goPcm) {
				sumL += float64(goPcm[idxL])
				sumR += float64(goPcm[idxR])
			}
		}
		dcL := sumL / float64(shortSize)
		dcR := sumR / float64(shortSize)
		t.Logf("  Block %d DC offset: L=%.6f, R=%.6f", b, dcL, dcR)
	}
}
