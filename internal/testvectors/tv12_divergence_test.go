package testvectors

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12FindFirstDivergingPacket finds the first packet where gopus diverges
// significantly from the libopus reference, particularly focusing on mode transitions.
func TestTV12FindFirstDivergingPacket(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	bitFile := filepath.Join(testVectorDir, "testvector12.bit")
	decFile := filepath.Join(testVectorDir, "testvector12.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse bitstream: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Fatalf("Failed to read reference: %v", err)
	}

	t.Logf("Total packets: %d", len(packets))
	t.Logf("Reference samples: %d", len(reference))

	dec, err := gopus.NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	type packetStats struct {
		index            int
		mode             string
		frameSize        int
		snr              float64
		maxDiff          int16
		avgDiff          float64
		isModeTransition bool
		prevMode         string
	}

	var stats []packetStats
	var decoded []int16
	prevMode := ""
	firstDivergingHybrid := -1

	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		tocByte := pkt.Data[0]
		cfg := tocByte >> 3
		fs := getFrameSizeFromConfig(cfg)
		mode := getModeFromConfig(cfg)

		isModeTransition := prevMode != "" && prevMode != mode

		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			t.Logf("Packet %d decode error: %v", i, err)
			zeros := make([]int16, fs*2)
			decoded = append(decoded, zeros...)
			prevMode = mode
			continue
		}
		decoded = append(decoded, pcm...)

		// Calculate stats for this packet
		refStart := len(decoded) - len(pcm)
		refEnd := len(decoded)
		if refEnd > len(reference) {
			refEnd = len(reference)
		}
		if refStart >= refEnd {
			prevMode = mode
			continue
		}

		var signalPower, noisePower float64
		var maxDiff int16
		var sumDiff float64
		count := 0

		for j := refStart; j < refEnd; j++ {
			decIdx := j - refStart + len(decoded) - len(pcm)
			if decIdx >= len(decoded) || j >= len(reference) {
				continue
			}
			d := decoded[decIdx]
			r := reference[j]
			diff := int32(d) - int32(r)
			if diff < 0 {
				diff = -diff
			}
			if int16(diff) > maxDiff {
				maxDiff = int16(diff)
			}
			sumDiff += float64(diff)
			signalPower += float64(r) * float64(r)
			noisePower += float64(d-r) * float64(d-r)
			count++
		}

		snr := float64(0)
		if signalPower > 0 && noisePower > 0 {
			snr = 10 * math.Log10(signalPower/noisePower)
		} else if noisePower == 0 {
			snr = 200 // Perfect match
		}

		avgDiff := float64(0)
		if count > 0 {
			avgDiff = sumDiff / float64(count)
		}

		ps := packetStats{
			index:            i,
			mode:             mode,
			frameSize:        fs,
			snr:              snr,
			maxDiff:          maxDiff,
			avgDiff:          avgDiff,
			isModeTransition: isModeTransition,
			prevMode:         prevMode,
		}
		stats = append(stats, ps)

		// Track first diverging Hybrid packet
		if mode == "Hybrid" && snr < 40 && firstDivergingHybrid < 0 {
			firstDivergingHybrid = i
		}

		prevMode = mode
	}

	// Report statistics
	t.Logf("\n=== Mode Distribution ===")
	silkCount := 0
	hybridCount := 0
	silkFails := 0
	hybridFails := 0

	for _, ps := range stats {
		if ps.mode == "SILK" {
			silkCount++
			if ps.snr < 40 {
				silkFails++
			}
		} else if ps.mode == "Hybrid" {
			hybridCount++
			if ps.snr < 40 {
				hybridFails++
			}
		}
	}

	t.Logf("SILK packets: %d, failing (SNR<40dB): %d (%.1f%%)", silkCount, silkFails, 100*float64(silkFails)/float64(silkCount))
	t.Logf("Hybrid packets: %d, failing (SNR<40dB): %d (%.1f%%)", hybridCount, hybridFails, 100*float64(hybridFails)/float64(hybridCount))

	// Report mode transitions
	t.Logf("\n=== Mode Transitions ===")
	for _, ps := range stats {
		if ps.isModeTransition {
			t.Logf("Packet %d: %s -> %s, SNR=%.1f dB, maxDiff=%d",
				ps.index, ps.prevMode, ps.mode, ps.snr, ps.maxDiff)
		}
	}

	// Report first diverging Hybrid packet with context
	if firstDivergingHybrid >= 0 {
		t.Logf("\n=== First Diverging Hybrid Packet: %d ===", firstDivergingHybrid)
		// Show packets around the first divergence
		start := firstDivergingHybrid - 5
		if start < 0 {
			start = 0
		}
		end := firstDivergingHybrid + 10
		if end > len(stats) {
			end = len(stats)
		}
		for i := start; i < end; i++ {
			ps := stats[i]
			marker := ""
			if i == firstDivergingHybrid {
				marker = " <-- FIRST DIVERGING HYBRID"
			}
			t.Logf("  Packet %d: mode=%s, fs=%d, SNR=%.1f dB, maxDiff=%d, avgDiff=%.1f%s",
				ps.index, ps.mode, ps.frameSize, ps.snr, ps.maxDiff, ps.avgDiff, marker)
		}
	}

	// Report worst packets by mode
	t.Logf("\n=== Worst SILK Packets (by SNR) ===")
	worstSILK := []packetStats{}
	for _, ps := range stats {
		if ps.mode == "SILK" && ps.snr < 60 {
			worstSILK = append(worstSILK, ps)
		}
	}
	// Sort by SNR ascending
	for i := 0; i < len(worstSILK) && i < 10; i++ {
		for j := i + 1; j < len(worstSILK); j++ {
			if worstSILK[j].snr < worstSILK[i].snr {
				worstSILK[i], worstSILK[j] = worstSILK[j], worstSILK[i]
			}
		}
	}
	for i := 0; i < len(worstSILK) && i < 10; i++ {
		ps := worstSILK[i]
		t.Logf("  Packet %d: SNR=%.1f dB, maxDiff=%d, transition=%v",
			ps.index, ps.snr, ps.maxDiff, ps.isModeTransition)
	}

	t.Logf("\n=== Worst Hybrid Packets (by SNR) ===")
	worstHybrid := []packetStats{}
	for _, ps := range stats {
		if ps.mode == "Hybrid" && ps.snr < 100 {
			worstHybrid = append(worstHybrid, ps)
		}
	}
	// Sort by SNR ascending
	for i := 0; i < len(worstHybrid) && i < 10; i++ {
		for j := i + 1; j < len(worstHybrid); j++ {
			if worstHybrid[j].snr < worstHybrid[i].snr {
				worstHybrid[i], worstHybrid[j] = worstHybrid[j], worstHybrid[i]
			}
		}
	}
	for i := 0; i < len(worstHybrid) && i < 15; i++ {
		ps := worstHybrid[i]
		t.Logf("  Packet %d: SNR=%.1f dB, maxDiff=%d, avgDiff=%.1f, transition=%v",
			ps.index, ps.snr, ps.maxDiff, ps.avgDiff, ps.isModeTransition)
	}
}
