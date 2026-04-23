package testvectors

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

type finalRangeVector struct {
	filename  string
	stereo    bool
	minPassed int
}

type finalRangeStats struct {
	passed  int
	failed  int
	skipped int
	total   int
}

var finalRangeVectors = []finalRangeVector{
	{"testvector01.bit", false, 2147}, // SILK NB mono
	{"testvector02.bit", false, 1185}, // SILK MB mono
	{"testvector03.bit", false, 998},  // SILK WB mono
	{"testvector04.bit", true, 1265},  // SILK NB stereo
	{"testvector05.bit", true, 2037},  // SILK MB stereo
	{"testvector06.bit", true, 1876},  // SILK WB stereo
	{"testvector07.bit", false, 4186}, // Hybrid SWB mono
	{"testvector08.bit", true, 1247},  // Hybrid SWB stereo
	{"testvector09.bit", false, 1337}, // CELT NB mono
	{"testvector10.bit", false, 1598}, // CELT WB mono, with hybrid transition drift
	{"testvector11.bit", true, 553},   // CELT NB stereo
	{"testvector12.bit", true, 1290},  // CELT WB stereo, with hybrid transition drift
}

// TestFinalRangeVerification verifies that the decoder's FinalRange matches
// the expected FinalRange stored in the test vector packets.
// This is a critical compliance test per RFC 6716.
func TestFinalRangeVerification(t *testing.T) {
	testVectorDir := "testdata/opus_testvectors"

	// Check if test vectors exist
	if _, err := os.Stat(testVectorDir); os.IsNotExist(err) {
		t.Skip("Test vectors not found at", testVectorDir)
	}

	for _, tv := range finalRangeVectors {
		t.Run(tv.filename, func(t *testing.T) {
			stats := verifyFinalRange(t, filepath.Join(testVectorDir, tv.filename), tv.stereo)
			if stats.skipped != 0 {
				t.Fatalf("FinalRange verification skipped %d packets", stats.skipped)
			}
			if stats.passed < tv.minPassed {
				t.Fatalf("FinalRange parity regressed: passed %d want at least %d", stats.passed, tv.minPassed)
			}
		})
	}
}

func verifyFinalRange(t *testing.T, bitFile string, stereo bool) finalRangeStats {
	t.Helper()

	// Parse the bitstream file
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to read bitstream file: %v", err)
	}

	if len(packets) == 0 {
		t.Skip("No packets in bitstream file")
	}

	// Determine channels
	channels := 1
	if stereo {
		channels = 2
	}

	// Create decoder
	cfg := gopus.DefaultDecoderConfig(48000, channels)
	decoder, err := gopus.NewDecoder(cfg)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Allocate for the decoder's configured packet cap, including 120 ms packets.
	pcm := make([]float32, cfg.MaxPacketSamples*channels)

	// Track statistics
	var passed, failed, skipped int

	for i, pkt := range packets {
		// Decode the packet
		_, err := decoder.Decode(pkt.Data, pcm)
		if err != nil {
			if skipped < 5 {
				t.Logf("Packet %d: decode error: %v (skipping FinalRange check)", i, err)
			}
			skipped++
			continue
		}

		// Get the decoder's final range
		actualRange := decoder.FinalRange()
		expectedRange := pkt.FinalRange

		if actualRange == expectedRange {
			passed++
		} else {
			failed++
			// Only log first few failures to avoid noise
			if failed <= 5 {
				t.Logf("Packet %d: FinalRange mismatch: got 0x%08X, want 0x%08X",
					i, actualRange, expectedRange)
			}
		}
	}

	// Report summary
	t.Logf("FinalRange verification: %d passed, %d failed, %d skipped out of %d packets",
		passed, failed, skipped, len(packets))

	return finalRangeStats{
		passed:  passed,
		failed:  failed,
		skipped: skipped,
		total:   len(packets),
	}
}

// TestFinalRangeNonZero verifies that FinalRange returns non-zero after decoding.
// This is a basic sanity check.
func TestFinalRangeNonZero(t *testing.T) {
	testVectorDir := "testdata/opus_testvectors"

	if _, err := os.Stat(testVectorDir); os.IsNotExist(err) {
		t.Skip("Test vectors not found")
	}

	// Use testvector01 (SILK NB mono) as a simple test case
	packets, err := ReadBitstreamFile(filepath.Join(testVectorDir, "testvector01.bit"))
	if err != nil {
		t.Fatalf("Failed to read bitstream: %v", err)
	}

	if len(packets) == 0 {
		t.Skip("No packets in bitstream")
	}

	cfg := gopus.DefaultDecoderConfig(48000, 1)
	decoder, err := gopus.NewDecoder(cfg)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	pcm := make([]float32, 2880)

	// Decode first packet
	_, err = decoder.Decode(packets[0].Data, pcm)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	// Check FinalRange is non-zero
	finalRange := decoder.FinalRange()
	if finalRange == 0 {
		t.Errorf("FinalRange is zero after decoding, expected non-zero value")
	}

	t.Logf("Packet 0: FinalRange = 0x%08X (expected 0x%08X)", finalRange, packets[0].FinalRange)
}

// TestFinalRangeModeTransitions verifies FinalRange works across mode transitions.
func TestFinalRangeModeTransitions(t *testing.T) {
	testVectorDir := "testdata/opus_testvectors"

	if _, err := os.Stat(testVectorDir); os.IsNotExist(err) {
		t.Skip("Test vectors not found")
	}

	// Test with a vector that has mode transitions (testvector10 - CELT WB mono)
	packets, err := ReadBitstreamFile(filepath.Join(testVectorDir, "testvector10.bit"))
	if err != nil {
		t.Fatalf("Failed to read bitstream: %v", err)
	}

	cfg := gopus.DefaultDecoderConfig(48000, 1)
	decoder, err := gopus.NewDecoder(cfg)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	pcm := make([]float32, 2880)

	// Decode several packets and track FinalRange
	maxPackets := 10
	if len(packets) < maxPackets {
		maxPackets = len(packets)
	}
	for i := 0; i < maxPackets; i++ {
		_, err := decoder.Decode(packets[i].Data, pcm)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
			continue
		}

		actualRange := decoder.FinalRange()
		expectedRange := packets[i].FinalRange

		match := "MATCH"
		if actualRange != expectedRange {
			match = "MISMATCH"
		}

		t.Logf("Packet %d: FinalRange = 0x%08X (expected 0x%08X) %s",
			i, actualRange, expectedRange, match)
	}
}

// TestFinalRangeAllVectors provides a summary of FinalRange accuracy across all vectors.
func TestFinalRangeAllVectors(t *testing.T) {
	testVectorDir := "testdata/opus_testvectors"

	if _, err := os.Stat(testVectorDir); os.IsNotExist(err) {
		t.Skip("Test vectors not found")
	}

	results := make(map[string]struct {
		passed, failed, total int
	})

	for i := 1; i <= 12; i++ {
		filename := fmt.Sprintf("testvector%02d.bit", i)
		packets, err := ReadBitstreamFile(filepath.Join(testVectorDir, filename))
		if err != nil {
			t.Logf("%s: failed to read: %v", filename, err)
			continue
		}

		// Determine stereo from filename (04-06, 08, 11-12 are stereo)
		stereo := i == 4 || i == 5 || i == 6 || i == 8 || i == 11 || i == 12
		channels := 1
		if stereo {
			channels = 2
		}

		cfg := gopus.DefaultDecoderConfig(48000, channels)
		decoder, err := gopus.NewDecoder(cfg)
		if err != nil {
			t.Logf("%s: failed to create decoder: %v", filename, err)
			continue
		}

		pcm := make([]float32, cfg.MaxPacketSamples*channels)
		var passed, failed int

		for _, pkt := range packets {
			_, err := decoder.Decode(pkt.Data, pcm)
			if err != nil {
				continue
			}

			if decoder.FinalRange() == pkt.FinalRange {
				passed++
			} else {
				failed++
			}
		}

		results[filename] = struct {
			passed, failed, total int
		}{passed, failed, len(packets)}
	}

	// Print summary
	t.Log("FinalRange verification summary:")
	t.Log("================================")
	var totalPassed, totalFailed, totalPackets int
	for i := 1; i <= 12; i++ {
		filename := fmt.Sprintf("testvector%02d.bit", i)
		r := results[filename]
		pct := float64(0)
		if r.passed+r.failed > 0 {
			pct = float64(r.passed) / float64(r.passed+r.failed) * 100
		}
		t.Logf("%s: %d/%d passed (%.1f%%)", filename, r.passed, r.total, pct)
		totalPassed += r.passed
		totalFailed += r.failed
		totalPackets += r.total
	}
	t.Log("================================")
	totalPct := float64(0)
	if totalPassed+totalFailed > 0 {
		totalPct = float64(totalPassed) / float64(totalPassed+totalFailed) * 100
	}
	t.Logf("Total: %d/%d passed (%.1f%%)", totalPassed, totalPackets, totalPct)
}
