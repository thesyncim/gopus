// short_block_trace_test.go - Trace each short block in transient frame
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

// TestTraceShortBlockOverlap traces the overlap between short blocks
func TestTraceShortBlockOverlap(t *testing.T) {
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

	// Decode packets 0-60 to build up state
	goDec, _ := gopus.NewDecoder(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	for i := 0; i < 61; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode packet 61 (transient) and trace
	pkt61 := packets[61]
	toc := gopus.ParseTOC(pkt61[0])
	t.Logf("Packet 61: frameSize=%d, stereo=%v, mode=%d", toc.FrameSize, toc.Stereo, toc.Mode)

	// Decode with gopus
	goPcm, _ := goDec.DecodeFloat32(pkt61)
	libPcm, libN := libDec.DecodeFloat(pkt61, 5760)

	// Compare output in segments (one per short block for transient)
	shortSize := toc.FrameSize / 8 // 8 short blocks for transient
	t.Logf("\nComparing output in %d-sample segments:", shortSize)
	t.Logf("Block | Start |  SNR (dB) | Max Diff | Avg Diff")
	t.Logf("------+-------+-----------+----------+---------")

	channels := 2
	if !toc.Stereo {
		channels = 1
	}

	for b := 0; b < 8; b++ {
		startSample := b * shortSize
		endSample := (b + 1) * shortSize
		if endSample > libN {
			endSample = libN
		}

		var sig, noise, maxDiff, sumDiff float64
		count := 0
		for i := startSample; i < endSample; i++ {
			for ch := 0; ch < channels; ch++ {
				idx := i*channels + ch
				if idx < len(goPcm) && idx < libN*channels {
					s := float64(libPcm[idx])
					g := float64(goPcm[idx])
					d := g - s
					sig += s * s
					noise += d * d
					if math.Abs(d) > maxDiff {
						maxDiff = math.Abs(d)
					}
					sumDiff += math.Abs(d)
					count++
				}
			}
		}
		snr := 10 * math.Log10(sig/noise)
		avgDiff := sumDiff / float64(count)
		t.Logf("  %d   | %5d | %9.1f | %.6f | %.6f", b, startSample, snr, maxDiff, avgDiff)
	}
}

// TestDirectTransientSynthesis directly tests the transient synthesis with known coefficients
func TestDirectTransientSynthesis(t *testing.T) {
	// Parse packet 61 to get the coefficients
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

	pkt61 := packets[61]
	toc := gopus.ParseTOC(pkt61[0])
	if toc.Mode != gopus.ModeCELT {
		t.Skip("Packet 61 is not CELT mode")
	}

	celtData := pkt61[1:]
	rd := &rangecoding.Decoder{}
	rd.Init(celtData)

	mode := celt.GetModeConfig(toc.FrameSize)

	// Parse header to check transient flag
	silence := false
	tell := rd.Tell()
	totalBits := len(celtData) * 8
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}

	if silence {
		t.Log("Frame is silent")
		return
	}

	// Postfilter
	if tell+16 <= totalBits {
		postfilter := rd.DecodeBit(1) == 1
		if postfilter {
			octave := int(rd.DecodeUniform(6))
			_ = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			_ = rd.DecodeRawBits(3)
			if rd.Tell()+2 <= totalBits {
				_ = rd.DecodeRawBits(2)
			}
		}
	}

	// Transient flag
	transient := false
	if mode.LM > 0 && rd.Tell()+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
	}

	t.Logf("Transient flag: %v (LM=%d)", transient, mode.LM)

	if transient {
		shortBlocks := 1 << mode.LM // 8 for LM=3
		shortSize := toc.FrameSize / shortBlocks
		t.Logf("Short blocks: %d, Short size: %d", shortBlocks, shortSize)
	}
}

// TestOverlapBufferContentComparison compares the overlap buffer content
func TestOverlapBufferContentComparison(t *testing.T) {
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

	// Decode packets 0-60
	goDec, _ := gopus.NewDecoder(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	for i := 0; i < 61; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Get overlap buffer BEFORE decoding packet 61
	goOverlapBefore := make([]float64, len(goDec.GetCELTDecoder().OverlapBuffer()))
	copy(goOverlapBefore, goDec.GetCELTDecoder().OverlapBuffer())

	t.Logf("Overlap buffer BEFORE packet 61:")
	t.Logf("  Length: %d", len(goOverlapBefore))
	if len(goOverlapBefore) > 0 {
		var energy float64
		for _, v := range goOverlapBefore {
			energy += v * v
		}
		t.Logf("  Energy: %.6f", energy)
		t.Logf("  First 5: [%.4f, %.4f, %.4f, %.4f, %.4f]",
			goOverlapBefore[0], goOverlapBefore[1], goOverlapBefore[2], goOverlapBefore[3], goOverlapBefore[4])
	}

	// Decode packet 61
	goPcm, _ := goDec.DecodeFloat32(packets[61])
	libPcm, libN := libDec.DecodeFloat(packets[61], 5760)

	// Get overlap buffer AFTER decoding packet 61
	goOverlapAfter := goDec.GetCELTDecoder().OverlapBuffer()

	t.Logf("\nOverlap buffer AFTER packet 61:")
	t.Logf("  Length: %d", len(goOverlapAfter))
	if len(goOverlapAfter) > 0 {
		var energy float64
		for _, v := range goOverlapAfter {
			energy += v * v
		}
		t.Logf("  Energy: %.6f", energy)
		t.Logf("  First 5: [%.4f, %.4f, %.4f, %.4f, %.4f]",
			goOverlapAfter[0], goOverlapAfter[1], goOverlapAfter[2], goOverlapAfter[3], goOverlapAfter[4])
	}

	// Compare first few samples of output
	t.Logf("\nFirst 10 output samples comparison:")
	for i := 0; i < 10 && i < len(goPcm) && i < libN*2; i++ {
		t.Logf("  [%d] go=%.6f, lib=%.6f, diff=%.6e",
			i, goPcm[i], libPcm[i], goPcm[i]-float32(libPcm[i]))
	}
}

// TestIsolatedShortBlockIMDCT is disabled - needs exported IMDCT function
func TestIsolatedShortBlockIMDCT(t *testing.T) {
	t.Skip("Needs exported IMDCT function")
}
