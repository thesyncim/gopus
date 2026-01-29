// Package cgo investigates packet 1000 in detail
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

// TestPacket1000SpecificAnalysis does deep analysis of packet 1000
func TestPacket1000SpecificAnalysis(t *testing.T) {
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

	// Sync decoders to packet 999
	goDec, _ := gopus.NewDecoder(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	defer libDec.Destroy()

	for i := 0; i < 1000; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Analyze packet 1000
	pkt := packets[1000]
	t.Logf("Packet 1000 raw bytes (%d bytes):", len(pkt))
	t.Logf("  Hex: %02X", pkt)

	// Decode header flags
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("\nTOC analysis:")
	t.Logf("  TOC byte: 0x%02X", pkt[0])
	t.Logf("  Mode: %d (CELT)", toc.Mode)
	t.Logf("  Frame size: %d samples (2.5ms)", toc.FrameSize)
	t.Logf("  Stereo: %v", toc.Stereo)

	// Create a range decoder to peek at frame flags
	rd := &rangecoding.Decoder{}
	celtData := pkt[1:] // Skip TOC byte
	rd.Init(celtData)

	totalBits := len(celtData) * 8
	tell := rd.Tell()

	t.Logf("\nRange decoder state:")
	t.Logf("  Total bits: %d", totalBits)
	t.Logf("  Initial tell: %d", tell)

	// Decode silence flag
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	t.Logf("  Silence: %v", silence)

	// For LM=0, transient is not encoded
	mode := celt.GetModeConfig(120)
	t.Logf("\nMode config (LM=0):")
	t.Logf("  LM: %d", mode.LM)
	t.Logf("  ShortBlocks: %d", mode.ShortBlocks)
	t.Logf("  EffBands: %d", mode.EffBands)

	// Decode postfilter
	if rd.Tell()+16 <= totalBits {
		pfFlag := rd.DecodeBit(1)
		if pfFlag == 1 {
			t.Logf("  Postfilter: enabled")
		} else {
			t.Logf("  Postfilter: disabled")
		}
	}

	// Decode intra flag
	if rd.Tell()+3 <= totalBits {
		intra := rd.DecodeBit(3)
		t.Logf("  Intra: %d", intra)
	}

	// Now decode with both decoders and compare samples
	t.Logf("\nDecoding packet 1000:")

	// Reset state before decode for comparison
	libMemBefore, _ := libDec.GetPreemphState()
	goStateBefore := goDec.GetCELTDecoder().PreemphState()
	t.Logf("  State before: go=%.6f, lib=%.6f, err=%.2e",
		goStateBefore[0], libMemBefore, math.Abs(goStateBefore[0]-float64(libMemBefore)))

	goPcm, _ := goDec.DecodeFloat32(pkt)
	libPcm, libN := libDec.DecodeFloat(pkt, 5760)

	// Compare sample by sample for first 20 samples
	t.Logf("\nSample comparison (first 20):")
	for i := 0; i < 20 && i < libN; i++ {
		diff := float64(goPcm[i]) - float64(libPcm[i])
		t.Logf("  [%3d] go=%+.8f lib=%+.8f diff=%+.2e",
			i, goPcm[i], libPcm[i], diff)
	}

	// Compare last 20 samples
	t.Logf("\nSample comparison (last 20):")
	for i := libN - 20; i < libN && i >= 0; i++ {
		diff := float64(goPcm[i]) - float64(libPcm[i])
		t.Logf("  [%3d] go=%+.8f lib=%+.8f diff=%+.2e",
			i, goPcm[i], libPcm[i], diff)
	}

	// Find where divergence starts
	t.Logf("\nFinding divergence point:")
	var firstBadSample int = -1
	var firstBadDiff float64
	for i := 0; i < libN; i++ {
		diff := math.Abs(float64(goPcm[i]) - float64(libPcm[i]))
		if diff > 1e-6 {
			firstBadSample = i
			firstBadDiff = diff
			break
		}
	}
	if firstBadSample >= 0 {
		t.Logf("  First sample with diff > 1e-6: sample %d (diff=%.2e)", firstBadSample, firstBadDiff)
	} else {
		t.Logf("  No samples with diff > 1e-6")
	}

	// State after
	libMemAfter, _ := libDec.GetPreemphState()
	goStateAfter := goDec.GetCELTDecoder().PreemphState()
	t.Logf("\n  State after: go=%.6f, lib=%.6f, err=%.2e",
		goStateAfter[0], libMemAfter, math.Abs(goStateAfter[0]-float64(libMemAfter)))
}

// TestPacket1000VsFreshDecode compares packet 1000 with synced vs fresh decoders
func TestPacket1000VsFreshDecode(t *testing.T) {
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

	pkt := packets[1000]

	// Test 1: Fresh decode
	t.Log("Test 1: Fresh decoders (no history)")
	goDec1, _ := gopus.NewDecoder(48000, 1)
	libDec1, _ := NewLibopusDecoder(48000, 1)

	goPcm1, _ := goDec1.DecodeFloat32(pkt)
	libPcm1, libN1 := libDec1.DecodeFloat(pkt, 5760)
	libDec1.Destroy()

	n1 := minInt(len(goPcm1), libN1)
	var sig1, noise1 float64
	for j := 0; j < n1; j++ {
		s := float64(libPcm1[j])
		d := float64(goPcm1[j]) - s
		sig1 += s * s
		noise1 += d * d
	}
	snr1 := 10 * math.Log10(sig1/noise1)
	t.Logf("  SNR: %.1f dB", snr1)

	// Test 2: With history
	t.Log("\nTest 2: Synced decoders (packets 0-999 as history)")
	goDec2, _ := gopus.NewDecoder(48000, 1)
	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()

	for i := 0; i < 1000; i++ {
		goDec2.DecodeFloat32(packets[i])
		libDec2.DecodeFloat(packets[i], 5760)
	}

	goPcm2, _ := goDec2.DecodeFloat32(pkt)
	libPcm2, libN2 := libDec2.DecodeFloat(pkt, 5760)

	n2 := minInt(len(goPcm2), libN2)
	var sig2, noise2 float64
	for j := 0; j < n2; j++ {
		s := float64(libPcm2[j])
		d := float64(goPcm2[j]) - s
		sig2 += s * s
		noise2 += d * d
	}
	snr2 := 10 * math.Log10(sig2/noise2)
	t.Logf("  SNR: %.1f dB", snr2)

	// Test 3: Just packets 996-999 as history (minimal 2.5ms context)
	t.Log("\nTest 3: Minimal history (packets 996-999 only)")
	goDec3, _ := gopus.NewDecoder(48000, 1)
	libDec3, _ := NewLibopusDecoder(48000, 1)
	defer libDec3.Destroy()

	for i := 996; i < 1000; i++ {
		goDec3.DecodeFloat32(packets[i])
		libDec3.DecodeFloat(packets[i], 5760)
	}

	goPcm3, _ := goDec3.DecodeFloat32(pkt)
	libPcm3, libN3 := libDec3.DecodeFloat(pkt, 5760)

	n3 := minInt(len(goPcm3), libN3)
	var sig3, noise3 float64
	for j := 0; j < n3; j++ {
		s := float64(libPcm3[j])
		d := float64(goPcm3[j]) - s
		sig3 += s * s
		noise3 += d * d
	}
	snr3 := 10 * math.Log10(sig3/noise3)
	t.Logf("  SNR: %.1f dB", snr3)
}
