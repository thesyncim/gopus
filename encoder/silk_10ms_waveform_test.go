package encoder

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestSILK10msWaveformCompare compares the actual decoded waveforms from opusdec
// for 10ms vs 20ms to identify the nature of the quality difference.
func TestSILK10msWaveformCompare(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Skip("opusdec not found")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			numFrames := 48000 / tc.frameSize
			var packets [][]byte

			for i := 0; i < numFrames; i++ {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := enc.Encode(pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)
			decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			if len(decoded) < 1000 {
				t.Fatal("not enough decoded samples")
			}

			// Check energy per 10ms block
			t.Logf("Total decoded samples: %d", len(decoded))

			// RMS per block (after pre-skip)
			preSkip := 312
			if preSkip < len(decoded) {
				decoded = decoded[preSkip:]
			}

			t.Logf("After pre-skip: %d samples (expected ~48000)", len(decoded))

			// Find the period of the decoded signal to check frequency
			// Look for zero crossings in decoded[1000:5000]
			var crossings int
			for i := 1001; i < 5000 && i < len(decoded); i++ {
				if (decoded[i-1] < 0 && decoded[i] >= 0) ||
					(decoded[i-1] >= 0 && decoded[i] < 0) {
					crossings++
				}
			}
			estimatedFreq := float64(crossings) * 48000.0 / (2.0 * 4000.0)
			t.Logf("Estimated frequency from zero crossings: %.1f Hz (expected 440)", estimatedFreq)

			// Check if it's roughly 440 Hz
			if estimatedFreq < 400 || estimatedFreq > 480 {
				t.Logf("WARNING: Frequency %0.1f is far from expected 440 Hz", estimatedFreq)
			}

			// Print first 20 samples after pre-skip
			t.Logf("First 20 samples after pre-skip:")
			for i := 0; i < 20 && i < len(decoded); i++ {
				t.Logf("  [%d] = %.6f", i, decoded[i])
			}

			// Print samples around the 3rd frame boundary (at sample 1440 for 10ms, 2880 for 20ms)
			boundaryIdx := 3 * tc.frameSize
			if boundaryIdx+10 < len(decoded) {
				t.Logf("Samples around frame boundary at %d:", boundaryIdx)
				for i := boundaryIdx - 5; i < boundaryIdx+5 && i >= 0 && i < len(decoded); i++ {
					t.Logf("  [%d] = %.6f", i, decoded[i])
				}
			}

			// Check for discontinuities at frame boundaries
			var maxDiscont float64
			var discontAt int
			for frame := 1; frame < numFrames && (frame+1)*tc.frameSize < len(decoded); frame++ {
				boundaryIdx := frame * tc.frameSize
				if boundaryIdx+1 < len(decoded) {
					// Check sample jump at boundary
					jump := math.Abs(float64(decoded[boundaryIdx]) - float64(decoded[boundaryIdx-1]))
					// Also check what the expected jump should be for a sine wave
					tm := float64(boundaryIdx) / 48000.0
					tmPrev := float64(boundaryIdx-1) / 48000.0
					expectedJump := math.Abs(0.5*math.Sin(2*math.Pi*440*tm) - 0.5*math.Sin(2*math.Pi*440*tmPrev))
					excess := jump - expectedJump
					if excess > maxDiscont {
						maxDiscont = excess
						discontAt = frame
					}
				}
			}
			t.Logf("Max discontinuity excess at frame boundaries: %.6f at frame %d", maxDiscont, discontAt)
		})
	}
}
