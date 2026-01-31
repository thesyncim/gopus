// state_compare_test.go - Compare internal state between gopus and libopus
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestLibopusStructAlignment verifies that our mirrored struct matches libopus
func TestLibopusStructAlignment(t *testing.T) {
	libDec, err := NewLibopusDecoder(48000, 2)
	if err != nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	defer libDec.Destroy()

	// These should return expected values if struct is aligned correctly
	overlap := libDec.GetCELTOverlap()
	channels := libDec.GetCELTChannels()

	t.Logf("CELT overlap: %d (expected 120)", overlap)
	t.Logf("CELT channels: %d (expected 2)", channels)

	if overlap != 120 {
		t.Errorf("CELT overlap mismatch: got %d, want 120", overlap)
	}
	if channels != 2 {
		t.Errorf("CELT channels mismatch: got %d, want 2", channels)
	}

	// Check initial preemph state (should be 0)
	mem0, mem1 := libDec.GetPreemphState()
	t.Logf("Initial preemph state: mem0=%.8f, mem1=%.8f", mem0, mem1)

	if mem0 != 0 || mem1 != 0 {
		t.Logf("Warning: Initial preemph state not zero (may be expected)")
	}
}

// TestPreemphStateComparison compares de-emphasis state after each packet
func TestPreemphStateComparison(t *testing.T) {
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

	goDec, _ := gopus.NewDecoderDefault(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	t.Log("Comparing de-emphasis state after each packet (first 70 packets):")

	var firstDivergencePacket int = -1
	var maxStateDiff float64

	for i := 0; i < 70 && i < len(packets); i++ {
		pkt := packets[i]

		// Decode with both decoders
		goDec.DecodeFloat32(pkt)
		libDec.DecodeFloat(pkt, 5760)

		// Get preemph state from both
		libMem0, libMem1 := libDec.GetPreemphState()
		goState := goDec.GetCELTDecoder().PreemphState()
		goMem0 := float32(goState[0])
		goMem1 := float32(goState[1])

		diff0 := math.Abs(float64(goMem0 - libMem0))
		diff1 := math.Abs(float64(goMem1 - libMem1))
		maxDiff := math.Max(diff0, diff1)

		if maxDiff > maxStateDiff {
			maxStateDiff = maxDiff
		}

		marker := ""
		if maxDiff > 0.001 {
			marker = " ***"
			if firstDivergencePacket < 0 {
				firstDivergencePacket = i
			}
		}

		// Log every 10 packets or on divergence
		if i%10 == 0 || maxDiff > 0.001 {
			t.Logf("Pkt %3d: go=[%.8f, %.8f] lib=[%.8f, %.8f] diff=[%.8f, %.8f]%s",
				i, goMem0, goMem1, libMem0, libMem1, diff0, diff1, marker)
		}
	}

	t.Logf("\nMax state difference: %.8f", maxStateDiff)
	if firstDivergencePacket >= 0 {
		t.Logf("First divergence at packet: %d", firstDivergencePacket)
	}

	if maxStateDiff > 0.001 {
		t.Errorf("De-emphasis state drift detected (max diff: %.8f)", maxStateDiff)
	}
}

// TestDetailedStateDivergence traces state divergence in detail
func TestDetailedStateDivergence(t *testing.T) {
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

	channels := 2

	goDec, _ := gopus.NewDecoderDefault(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Track cumulative sample differences
	var totalSampleDiff float64
	var sampleCount int

	t.Log("Detailed analysis of packets 55-65 (around first transient):")

	for i := 0; i < 70 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, _ := goDec.DecodeFloat32(pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		// Get state
		libMem0, libMem1 := libDec.GetPreemphState()
		goState := goDec.GetCELTDecoder().PreemphState()

		// Compute SNR for this frame
		var sig, noise float64
		n := minInt(len(goPcm), libSamples*channels)
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
			totalSampleDiff += math.Abs(d)
			sampleCount++
		}

		snr := 10 * math.Log10(sig/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		// Show details for packets 55-65
		if i >= 55 && i <= 65 {
			stateDiff0 := math.Abs(goState[0] - float64(libMem0))
			stateDiff1 := math.Abs(goState[1] - float64(libMem1))

			st := "mono"
			if toc.Stereo {
				st = "stereo"
			}

			t.Logf("\n=== Packet %d: frame=%d %s len=%d ===", i, toc.FrameSize, st, len(pkt))
			t.Logf("  SNR: %.1f dB", snr)
			t.Logf("  go preemph:  [%.8f, %.8f]", goState[0], goState[1])
			t.Logf("  lib preemph: [%.8f, %.8f]", libMem0, libMem1)
			t.Logf("  state diff:  [%.8f, %.8f]", stateDiff0, stateDiff1)

			// Show first 8 samples
			t.Log("  First 8 samples (L/R interleaved):")
			for j := 0; j < 8 && j < n; j++ {
				d := float64(goPcm[j]) - float64(libPcm[j])
				ch := "L"
				if j%2 == 1 {
					ch = "R"
				}
				marker := ""
				if math.Abs(d) > 0.001 {
					marker = " *"
				}
				t.Logf("    [%d %s] go=%+.8f lib=%+.8f diff=%+.8f%s",
					j/2, ch, goPcm[j], libPcm[j], d, marker)
			}
		}
	}

	t.Logf("\nTotal samples: %d, avg diff: %.8f", sampleCount, totalSampleDiff/float64(sampleCount))
}

// TestOverlapBufferComparison compares overlap buffer state
func TestOverlapBufferComparison(t *testing.T) {
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

	channels := 2

	// Decode with fresh decoder to check the first transient packet
	goDec, _ := gopus.NewDecoderDefault(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Decode up to packet 60 (the packet before first transient)
	for i := 0; i <= 60; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Get overlap buffer from gopus
	goOverlap := goDec.GetCELTDecoder().OverlapBuffer()
	t.Logf("gopus overlap buffer length: %d", len(goOverlap))
	t.Logf("First 10 overlap values: %v", goOverlap[:10])
	t.Logf("Last 10 overlap values: %v", goOverlap[len(goOverlap)-10:])

	// Get preemph state
	libMem0, libMem1 := libDec.GetPreemphState()
	goState := goDec.GetCELTDecoder().PreemphState()
	t.Logf("After 61 packets:")
	t.Logf("  gopus preemph:  [%.8f, %.8f]", goState[0], goState[1])
	t.Logf("  libopus preemph: [%.8f, %.8f]", libMem0, libMem1)

	// Now decode packet 61 (first transient)
	pkt61 := packets[61]
	toc := gopus.ParseTOC(pkt61[0])
	t.Logf("\nPacket 61: frame=%d stereo=%v len=%d", toc.FrameSize, toc.Stereo, len(pkt61))

	goPcm61, _ := goDec.DecodeFloat32(pkt61)
	libPcm61, libSamples := libDec.DecodeFloat(pkt61, 5760)

	// Compare samples
	var sig, noise float64
	n := minInt(len(goPcm61), libSamples*channels)
	for j := 0; j < n; j++ {
		s := float64(libPcm61[j])
		d := float64(goPcm61[j]) - s
		sig += s * s
		noise += d * d
	}
	snr := 10 * math.Log10(sig/noise)
	t.Logf("Packet 61 SNR: %.1f dB", snr)

	// Show final state
	libMem0After, libMem1After := libDec.GetPreemphState()
	goStateAfter := goDec.GetCELTDecoder().PreemphState()
	t.Logf("After packet 61:")
	t.Logf("  gopus preemph:  [%.8f, %.8f]", goStateAfter[0], goStateAfter[1])
	t.Logf("  libopus preemph: [%.8f, %.8f]", libMem0After, libMem1After)
}
