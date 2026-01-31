// transient_detail_test.go - Detailed analysis of transient frame synthesis
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

// TestTransientSynthesisDetail traces exactly where transient synthesis diverges
func TestTransientSynthesisDetail(t *testing.T) {
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
		offset += 4 // skip enc_final_range
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	channels := 2

	// Decode packets 0-60 to build up state
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	for i := 0; i < 61 && i < len(packets); i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Get state before packet 61
	libMem0Before, libMem1Before := libDec.GetPreemphState()
	goStateBefore := goDec.GetCELTDecoder().PreemphState()
	goOverlapBefore := goDec.GetCELTDecoder().OverlapBuffer()

	t.Logf("State before packet 61 (first transient):")
	t.Logf("  gopus preemph:  [%.8f, %.8f]", goStateBefore[0], goStateBefore[1])
	t.Logf("  libopus preemph: [%.8f, %.8f]", libMem0Before, libMem1Before)
	t.Logf("  gopus overlap first 10: %v", goOverlapBefore[:10])
	t.Logf("  gopus overlap last 10: %v", goOverlapBefore[len(goOverlapBefore)-10:])

	// Now decode packet 61
	pkt61 := packets[61]
	toc := gopus.ParseTOC(pkt61[0])
	t.Logf("\nPacket 61: frame=%d stereo=%v len=%d mode=%v", toc.FrameSize, toc.Stereo, len(pkt61), toc.Mode)

	// Parse transient flag
	celtData := pkt61[1:]
	rd := &rangecoding.Decoder{}
	rd.Init(celtData)
	mode := celt.GetModeConfig(toc.FrameSize)
	lm := mode.LM

	// Check silence/postfilter/transient flags
	totalBits := len(celtData) * 8
	tell := rd.Tell()

	silence := tell >= totalBits
	if !silence && tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}

	if !silence && tell+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniform(6))
			_ = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			_ = rd.DecodeRawBits(3)
			if rd.Tell()+2 <= totalBits {
				_ = rd.DecodeRawBits(2)
			}
		}
		tell = rd.Tell()
	}

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
	}

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	t.Logf("  silence=%v transient=%v shortBlocks=%d lm=%d", silence, transient, shortBlocks, lm)

	// Decode with both
	goPcm61, _ := goDec.DecodeFloat32(pkt61)
	libPcm61, libSamples := libDec.DecodeFloat(pkt61, 5760)

	// Compare in sections for transient frames (8 short blocks of 120 samples each for stereo = 240 samples)
	if transient && shortBlocks == 8 {
		shortSize := toc.FrameSize / shortBlocks // 120 samples
		t.Logf("\nComparing %d short blocks of %d samples each:", shortBlocks, shortSize)

		for b := 0; b < shortBlocks; b++ {
			startIdx := b * shortSize * channels
			endIdx := (b + 1) * shortSize * channels
			if endIdx > len(goPcm61) {
				endIdx = len(goPcm61)
			}
			if endIdx > libSamples*channels {
				endIdx = libSamples * channels
			}

			var sig, noise float64
			var maxDiff float64
			var maxDiffIdx int
			for i := startIdx; i < endIdx; i++ {
				s := float64(libPcm61[i])
				d := float64(goPcm61[i]) - s
				sig += s * s
				noise += d * d
				if math.Abs(d) > maxDiff {
					maxDiff = math.Abs(d)
					maxDiffIdx = i - startIdx
				}
			}

			snr := 10 * math.Log10(sig/noise)
			if math.IsNaN(snr) || math.IsInf(snr, 1) {
				snr = 999
			}

			t.Logf("  Block %d [%d:%d]: SNR=%.1f dB, maxDiff=%.6f at sample %d",
				b, startIdx, endIdx, snr, maxDiff, maxDiffIdx)

			// Show first few samples of each block
			if b < 3 || snr < 50 {
				t.Log("    First 8 samples (interleaved L/R):")
				for j := 0; j < 8 && startIdx+j < len(goPcm61) && startIdx+j < libSamples*channels; j++ {
					idx := startIdx + j
					diff := float64(goPcm61[idx]) - float64(libPcm61[idx])
					ch := "L"
					if j%2 == 1 {
						ch = "R"
					}
					t.Logf("      [%d %s] go=%+.8f lib=%+.8f diff=%+.8f",
						j/2, ch, goPcm61[idx], libPcm61[idx], diff)
				}
			}
		}
	}

	// Overall SNR
	var sig, noise float64
	n := minInt(len(goPcm61), libSamples*channels)
	for j := 0; j < n; j++ {
		s := float64(libPcm61[j])
		d := float64(goPcm61[j]) - s
		sig += s * s
		noise += d * d
	}
	snr := 10 * math.Log10(sig/noise)
	t.Logf("\nOverall packet 61 SNR: %.1f dB", snr)

	// Get state after
	libMem0After, libMem1After := libDec.GetPreemphState()
	goStateAfter := goDec.GetCELTDecoder().PreemphState()
	t.Logf("\nState after packet 61:")
	t.Logf("  gopus preemph:  [%.8f, %.8f]", goStateAfter[0], goStateAfter[1])
	t.Logf("  libopus preemph: [%.8f, %.8f]", libMem0After, libMem1After)
	t.Logf("  preemph diff: [%.8f, %.8f]",
		math.Abs(goStateAfter[0]-float64(libMem0After)),
		math.Abs(goStateAfter[1]-float64(libMem1After)))
}

// TestCompareIMDCTOutputShortBlock compares IMDCT output for short blocks between gopus and libopus
func TestCompareIMDCTOutputShortBlock(t *testing.T) {
	// This test compares IMDCT output directly by decoding a packet
	// that uses short blocks and comparing the pre-deemphasis samples
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

	// Get fresh decoders
	goDec, _ := gopus.NewDecoder(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	// Decode all packets before the first transient to sync state
	for i := 0; i < 61 && i < len(packets); i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now analyze packet 61 step by step
	// We want to compare the state AFTER decoding to understand what happened
	pkt := packets[61]
	t.Logf("Analyzing packet 61 (len=%d)", len(pkt))

	// Get the de-emphasis state before and after
	libMem0Before, libMem1Before := libDec.GetPreemphState()
	goStateBefore := goDec.GetCELTDecoder().PreemphState()

	t.Logf("Before decoding packet 61:")
	t.Logf("  Go preemph: [%.10f, %.10f]", goStateBefore[0], goStateBefore[1])
	t.Logf("  Lib preemph: [%.10f, %.10f]", libMem0Before, libMem1Before)

	// Decode
	goPcm, _ := goDec.DecodeFloat32(pkt)
	libPcm, libN := libDec.DecodeFloat(pkt, 5760)

	// Get state after
	libMem0After, libMem1After := libDec.GetPreemphState()
	goStateAfter := goDec.GetCELTDecoder().PreemphState()

	t.Logf("After decoding packet 61:")
	t.Logf("  Go preemph: [%.10f, %.10f]", goStateAfter[0], goStateAfter[1])
	t.Logf("  Lib preemph: [%.10f, %.10f]", libMem0After, libMem1After)
	t.Logf("  Delta: [%.10e, %.10e]",
		goStateAfter[0]-float64(libMem0After),
		goStateAfter[1]-float64(libMem1After))

	// Compare outputs
	n := minInt(len(goPcm), libN*2)
	var maxDiff float64
	var maxDiffIdx int
	for i := 0; i < n; i++ {
		diff := math.Abs(float64(goPcm[i]) - float64(libPcm[i]))
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}
	t.Logf("Max output diff: %.8f at sample %d (of %d)", maxDiff, maxDiffIdx, n)

	// Show around the max diff
	if maxDiff > 0.001 {
		t.Log("Samples around max diff:")
		start := maxDiffIdx - 4
		if start < 0 {
			start = 0
		}
		for i := start; i < start+10 && i < n; i++ {
			diff := float64(goPcm[i]) - float64(libPcm[i])
			ch := "L"
			if i%2 == 1 {
				ch = "R"
			}
			t.Logf("  [%d %s] go=%.8f lib=%.8f diff=%+.8f", i/2, ch, goPcm[i], libPcm[i], diff)
		}
	}
}

// TestOverlapBufferTransient specifically tests the overlap buffer handling for transient frames
func TestOverlapBufferTransient(t *testing.T) {
	// We need to understand exactly what the overlap buffer looks like
	// before and after a transient frame

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

	goDec, _ := gopus.NewDecoder(48000, 2)

	// Decode up to packet 60
	for i := 0; i <= 60 && i < len(packets); i++ {
		goDec.DecodeFloat32(packets[i])
	}

	// Get overlap buffer before packet 61
	overlapBefore := make([]float64, len(goDec.GetCELTDecoder().OverlapBuffer()))
	copy(overlapBefore, goDec.GetCELTDecoder().OverlapBuffer())

	t.Logf("Overlap buffer before packet 61 (len=%d):", len(overlapBefore))
	t.Logf("  First 10: %v", overlapBefore[:10])
	t.Logf("  Last 10: %v", overlapBefore[len(overlapBefore)-10:])

	// Get energy of overlap buffer
	var energyBefore float64
	for _, v := range overlapBefore {
		energyBefore += v * v
	}
	t.Logf("  Energy: %.6f", energyBefore)

	// Decode packet 61
	goDec.DecodeFloat32(packets[61])

	// Get overlap buffer after
	overlapAfter := goDec.GetCELTDecoder().OverlapBuffer()

	t.Logf("Overlap buffer after packet 61:")
	t.Logf("  First 10: %v", overlapAfter[:10])
	t.Logf("  Last 10: %v", overlapAfter[len(overlapAfter)-10:])

	var energyAfter float64
	for _, v := range overlapAfter {
		energyAfter += v * v
	}
	t.Logf("  Energy: %.6f", energyAfter)

	// Compare
	var sumDiff float64
	for i := 0; i < len(overlapBefore) && i < len(overlapAfter); i++ {
		sumDiff += math.Abs(overlapBefore[i] - overlapAfter[i])
	}
	t.Logf("Sum of overlap buffer differences: %.6f", sumDiff)
}
