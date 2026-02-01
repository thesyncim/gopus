package testvectors

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV06PacketTransition1497 investigates the sudden quality drop at packet 1497.
// Based on previous analysis, quality is good (Q>10) through packet 1496, then
// suddenly drops with maxDiff jumping from 1 to 200+ at packet 1497-1499.
func TestTV06PacketTransition1497(t *testing.T) {
	bitFile := filepath.Join(testVectorDir, "testvector06.bit")
	decFile := filepath.Join(testVectorDir, "testvector06.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("Could not read test vector: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skipf("Could not read reference: %v", err)
	}

	dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))

	// Track per-packet stats and look for mode transitions
	refOffset := 0
	prevConfig := byte(255) // Invalid sentinel

	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		toc := pkt.Data[0]
		config := toc >> 3
		stereo := (toc & 0x04) != 0
		frameSize := getFrameSizeFromConfig(config)
		mode := getModeFromConfig(config)

		// Detect config/mode transitions
		if config != prevConfig && prevConfig != 255 {
			prevMode := getModeFromConfig(prevConfig)
			t.Logf("*** CONFIG CHANGE at packet %d: %d (%s) -> %d (%s)", i, prevConfig, prevMode, config, mode)
		}

		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
			prevConfig = config
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		// Calculate packet stats
		var sigPow, noisePow float64
		var maxDiff float64
		maxDiffIdx := 0
		for j := 0; j < len(pcm); j++ {
			sig := float64(refSlice[j])
			noise := float64(pcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			if math.Abs(noise) > maxDiff {
				maxDiff = math.Abs(noise)
				maxDiffIdx = j
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		q := (snr - 48.0) * (100.0 / 48.0)

		// Log packets around the transition (1494-1505)
		if i >= 1494 && i <= 1505 {
			t.Logf("Packet %4d: Q=%8.2f, maxDiff=%6.0f (idx=%4d), len=%3d, config=%2d, mode=%s, fs=%d, stereo=%v",
				i, q, maxDiff, maxDiffIdx, len(pkt.Data), config, mode, frameSize, stereo)

			// If this is a bad packet, show sample comparison around maxDiff
			if maxDiff > 50 {
				t.Logf("  Samples around max diff (idx %d):", maxDiffIdx)
				start := maxDiffIdx - 3
				if start < 0 {
					start = 0
				}
				end := maxDiffIdx + 4
				if end > len(pcm) {
					end = len(pcm)
				}
				for j := start; j < end && j < len(refSlice); j++ {
					marker := ""
					if j == maxDiffIdx {
						marker = " <-- MAX"
					}
					t.Logf("    [%4d] dec=%6d ref=%6d diff=%6d%s", j, pcm[j], refSlice[j], int(pcm[j])-int(refSlice[j]), marker)
				}
			}
		}

		prevConfig = config
		refOffset += len(pcm)
	}
}

// TestTV06ModeTransitions finds all mode/config transitions in testvector06.
func TestTV06ModeTransitions(t *testing.T) {
	bitFile := filepath.Join(testVectorDir, "testvector06.bit")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("Could not read test vector: %v", err)
	}

	t.Logf("Total packets: %d", len(packets))

	prevConfig := byte(255) // Invalid sentinel
	var transitions []int

	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		toc := pkt.Data[0]
		config := toc >> 3

		if config != prevConfig && prevConfig != 255 {
			prevMode := getModeFromConfig(prevConfig)
			mode := getModeFromConfig(config)
			prevFS := getFrameSizeFromConfig(prevConfig)
			fs := getFrameSizeFromConfig(config)
			t.Logf("Packet %4d: config %2d (%s, %dms) -> config %2d (%s, %dms)",
				i, prevConfig, prevMode, prevFS/48, config, mode, fs/48)
			transitions = append(transitions, i)
		}

		prevConfig = config
	}

	t.Logf("Total transitions: %d", len(transitions))
	t.Logf("Transition packet indices: %v", transitions)

	// Check if packet 1497 is near a transition
	for _, tr := range transitions {
		if tr >= 1490 && tr <= 1510 {
			t.Logf("!!! Transition at packet %d is near the quality drop at 1497", tr)
		}
	}
}

// TestTV06DetailedQuality computes quality in windows to find where degradation occurs.
func TestTV06DetailedQuality(t *testing.T) {
	bitFile := filepath.Join(testVectorDir, "testvector06.bit")
	decFile := filepath.Join(testVectorDir, "testvector06.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("Could not read test vector: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skipf("Could not read reference: %v", err)
	}

	dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))

	var allDecoded []int16
	for _, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}
		pcm, _ := decodeInt16(dec, pkt.Data)
		allDecoded = append(allDecoded, pcm...)
	}

	// Calculate quality in windows
	windowSamples := 48000 * 2 // 1 second windows (stereo)
	t.Logf("Window size: %d samples (1 second stereo)", windowSamples)

	for start := 0; start+windowSamples <= len(allDecoded) && start+windowSamples <= len(reference); start += windowSamples {
		window := allDecoded[start : start+windowSamples]
		refWindow := reference[start : start+windowSamples]

		q := ComputeQuality(window, refWindow, 48000)
		secondStart := start / (48000 * 2)
		t.Logf("Seconds %3d-%3d: Q=%.2f", secondStart, secondStart+1, q)
	}
}

// TestTV06RangeDecoderStateDrift checks if there's accumulated state drift.
func TestTV06RangeDecoderStateDrift(t *testing.T) {
	bitFile := filepath.Join(testVectorDir, "testvector06.bit")
	decFile := filepath.Join(testVectorDir, "testvector06.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("Could not read test vector: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skipf("Could not read reference: %v", err)
	}

	// Decode starting from different points to see if state matters
	testPoints := []int{0, 1000, 1400, 1450, 1490, 1495}

	for _, startPkt := range testPoints {
		dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))

		// Decode from start point
		var decoded []int16
		refOffset := 0

		// Skip reference offset to match start point
		for i := 0; i < startPkt && i < len(packets); i++ {
			pkt := packets[i]
			if len(pkt.Data) == 0 {
				continue
			}
			toc := pkt.Data[0]
			config := toc >> 3
			frameSize := getFrameSizeFromConfig(config)
			// TOC code determines frame count
			frameCode := toc & 0x03
			frameCount := 1
			switch frameCode {
			case 0:
				frameCount = 1
			case 1, 2:
				frameCount = 2
			case 3:
				if len(pkt.Data) > 1 {
					frameCount = int(pkt.Data[1] & 0x3F)
					if frameCount == 0 {
						frameCount = 1
					}
				}
			}
			refOffset += frameSize * frameCount * 2 // stereo
		}

		// Decode 20 packets from start point
		for i := startPkt; i < startPkt+20 && i < len(packets); i++ {
			pkt := packets[i]
			if len(pkt.Data) == 0 {
				continue
			}
			pcm, err := decodeInt16(dec, pkt.Data)
			if err != nil {
				continue
			}
			decoded = append(decoded, pcm...)
		}

		// Compare with reference
		if refOffset < len(reference) {
			endRef := refOffset + len(decoded)
			if endRef > len(reference) {
				endRef = len(reference)
			}
			if endRef > refOffset {
				refSlice := reference[refOffset:endRef]
				cmpLen := len(decoded)
				if len(refSlice) < cmpLen {
					cmpLen = len(refSlice)
				}

				var sigPow, noisePow float64
				var maxDiff float64
				for j := 0; j < cmpLen; j++ {
					sig := float64(refSlice[j])
					noise := float64(decoded[j]) - sig
					sigPow += sig * sig
					noisePow += noise * noise
					if math.Abs(noise) > maxDiff {
						maxDiff = math.Abs(noise)
					}
				}

				snr := 10 * math.Log10(sigPow/noisePow)
				q := (snr - 48.0) * (100.0 / 48.0)
				t.Logf("Starting at packet %4d: Q=%.2f, maxDiff=%.0f (samples compared: %d)", startPkt, q, maxDiff, cmpLen)
			}
		}
	}
}
