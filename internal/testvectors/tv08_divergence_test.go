package testvectors

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV08FindFirstDivergence finds the first packet where gopus diverges from libopus reference.
func TestTV08FindFirstDivergence(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	name := "testvector08"
	bitFile := filepath.Join(testVectorDir, name+".bit")
	decFile := filepath.Join(testVectorDir, name+".dec")

	// Parse .bit file
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", bitFile, err)
	}

	// Read reference file (libopus output)
	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Fatalf("Failed to read reference %s: %v", decFile, err)
	}

	t.Logf("TV08: %d packets, %d reference samples", len(packets), len(reference))

	// Create stereo decoder
	dec, err := gopus.NewDecoder(48000, 2)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Track sample offset in reference
	refOffset := 0
	var allDecoded []int16

	// Analyze each packet
	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		// Parse TOC
		tocByte := pkt.Data[0]
		cfg := tocByte >> 3
		stereo := (tocByte & 0x04) != 0
		frameCode := tocByte & 0x03
		mode := getModeFromConfig(cfg)
		frameSize := getFrameSizeFromConfig(cfg)

		// Get frame count
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

		// Expected samples for this packet (stereo = 2 channels)
		expectedSamples := frameSize * frameCount * 2

		// Decode packet
		pcm, err := dec.DecodeInt16Slice(pkt.Data)
		if err != nil {
			t.Logf("Packet %d: DECODE ERROR - %v (mode=%s, cfg=%d, stereo=%v)", i, err, mode, cfg, stereo)
			// Use zeros for failed packets
			pcm = make([]int16, expectedSamples)
		}

		// Compare this packet's output to reference
		if refOffset+len(pcm) <= len(reference) {
			refSegment := reference[refOffset : refOffset+len(pcm)]

			// Compute MSE for this packet
			var mse float64
			maxDiff := int16(0)
			maxDiffIdx := 0
			for j := 0; j < len(pcm); j++ {
				diff := int16(pcm[j] - refSegment[j])
				if diff < 0 {
					diff = -diff
				}
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = j
				}
				d := float64(pcm[j]) - float64(refSegment[j])
				mse += d * d
			}
			mse /= float64(len(pcm))
			rmse := math.Sqrt(mse)

			// Report if significant divergence
			if rmse > 100 || maxDiff > 500 {
				t.Logf("DIVERGENCE at packet %d:", i)
				t.Logf("  Mode=%s, Config=%d, Stereo=%v, FrameSize=%d, FrameCount=%d",
					mode, cfg, stereo, frameSize, frameCount)
				t.Logf("  RMSE=%.2f, MaxDiff=%d at sample %d", rmse, maxDiff, maxDiffIdx)
				t.Logf("  Sample offset in reference: %d", refOffset)
				t.Logf("  Decoded samples: %d, Expected: %d", len(pcm), expectedSamples)

				// Show first few samples comparison
				t.Logf("  First 10 samples (gopus vs ref):")
				for j := 0; j < 10 && j < len(pcm); j++ {
					t.Logf("    [%d] gopus=%6d, ref=%6d, diff=%6d", j, pcm[j], refSegment[j], pcm[j]-refSegment[j])
				}

				// Show samples around max diff
				t.Logf("  Samples around max diff (idx %d):", maxDiffIdx)
				start := maxDiffIdx - 2
				if start < 0 {
					start = 0
				}
				end := maxDiffIdx + 3
				if end > len(pcm) {
					end = len(pcm)
				}
				for j := start; j < end; j++ {
					t.Logf("    [%d] gopus=%6d, ref=%6d, diff=%6d", j, pcm[j], refSegment[j], pcm[j]-refSegment[j])
				}

				// Find previous packet mode for context
				if i > 0 {
					prevToc := packets[i-1].Data[0]
					prevCfg := prevToc >> 3
					prevMode := getModeFromConfig(prevCfg)
					prevStereo := (prevToc & 0x04) != 0
					t.Logf("  Previous packet: Mode=%s, Config=%d, Stereo=%v", prevMode, prevCfg, prevStereo)

					// Check if this is a mode transition
					if prevMode != mode {
						t.Logf("  *** MODE TRANSITION: %s -> %s ***", prevMode, mode)
					}
				}

				// Report only first divergence
				t.Logf("\n=== First divergence found at packet %d ===", i)
				return
			}
		}

		allDecoded = append(allDecoded, pcm...)
		refOffset += len(pcm)
	}

	t.Logf("No significant divergence found in %d packets", len(packets))
}

// TestTV08AnalyzePacketDistribution shows packet mode distribution for TV08.
func TestTV08AnalyzePacketDistribution(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	name := "testvector08"
	bitFile := filepath.Join(testVectorDir, name+".bit")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", bitFile, err)
	}

	t.Logf("TV08: %d total packets\n", len(packets))

	// Show first 20 packets with mode info
	t.Logf("First 20 packets:")
	for i := 0; i < 20 && i < len(packets); i++ {
		pkt := packets[i]
		if len(pkt.Data) == 0 {
			t.Logf("  [%3d] empty", i)
			continue
		}

		toc := pkt.Data[0]
		cfg := toc >> 3
		stereo := (toc & 0x04) != 0
		frameCode := toc & 0x03
		mode := getModeFromConfig(cfg)
		frameSize := getFrameSizeFromConfig(cfg)

		t.Logf("  [%3d] %s cfg=%2d stereo=%v fs=%4d fc=%d bytes=%d",
			i, mode, cfg, stereo, frameSize, frameCode, len(pkt.Data))
	}

	// Show mode transitions
	t.Logf("\nMode transitions (first 10):")
	transCount := 0
	for i := 1; i < len(packets) && transCount < 10; i++ {
		if len(packets[i].Data) == 0 || len(packets[i-1].Data) == 0 {
			continue
		}

		prevMode := getModeFromConfig(packets[i-1].Data[0] >> 3)
		currMode := getModeFromConfig(packets[i].Data[0] >> 3)

		if prevMode != currMode {
			prevStereo := (packets[i-1].Data[0] & 0x04) != 0
			currStereo := (packets[i].Data[0] & 0x04) != 0
			t.Logf("  [%3d] %s(s=%v) -> %s(s=%v)", i, prevMode, prevStereo, currMode, currStereo)
			transCount++
		}
	}
}
