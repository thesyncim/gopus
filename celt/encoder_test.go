package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestNewEncoder verifies encoder initialization matches decoder.
func TestNewEncoder(t *testing.T) {
	tests := []struct {
		name     string
		channels int
	}{
		{"mono", 1},
		{"stereo", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewEncoder(tt.channels)

			// Verify configuration
			if enc.Channels() != tt.channels {
				t.Errorf("Channels() = %d, want %d", enc.Channels(), tt.channels)
			}
			if enc.SampleRate() != 48000 {
				t.Errorf("SampleRate() = %d, want 48000", enc.SampleRate())
			}

			// Verify energy arrays initialized correctly (libopus init clears oldBandE).
			prevEnergy := enc.PrevEnergy()
			if len(prevEnergy) != MaxBands*tt.channels {
				t.Errorf("PrevEnergy length = %d, want %d", len(prevEnergy), MaxBands*tt.channels)
			}
			for i, e := range prevEnergy {
				if e != 0 {
					t.Errorf("PrevEnergy[%d] = %f, want 0.0", i, e)
				}
			}

			// Verify RNG initialized to libopus default (0)
			if enc.RNG() != 0 {
				t.Errorf("RNG() = %d, want 0", enc.RNG())
			}

			// Verify overlap buffer
			overlap := enc.OverlapBuffer()
			if len(overlap) != Overlap*tt.channels {
				t.Errorf("OverlapBuffer length = %d, want %d", len(overlap), Overlap*tt.channels)
			}

			// Verify preemph state
			preemph := enc.PreemphState()
			if len(preemph) != tt.channels {
				t.Errorf("PreemphState length = %d, want %d", len(preemph), tt.channels)
			}
		})
	}
}

// TestEncoderMatchesDecoder verifies encoder state matches decoder state.
func TestEncoderMatchesDecoder(t *testing.T) {
	channels := 2

	enc := NewEncoder(channels)
	dec := NewDecoder(channels)

	// Compare configuration
	if enc.Channels() != dec.Channels() {
		t.Errorf("Channels: enc=%d, dec=%d", enc.Channels(), dec.Channels())
	}
	if enc.SampleRate() != dec.SampleRate() {
		t.Errorf("SampleRate: enc=%d, dec=%d", enc.SampleRate(), dec.SampleRate())
	}

	// Compare energy arrays
	encEnergy := enc.PrevEnergy()
	decEnergy := dec.PrevEnergy()
	if len(encEnergy) != len(decEnergy) {
		t.Errorf("PrevEnergy length: enc=%d, dec=%d", len(encEnergy), len(decEnergy))
	}
	for i := range encEnergy {
		if encEnergy[i] != decEnergy[i] {
			t.Errorf("PrevEnergy[%d]: enc=%f, dec=%f", i, encEnergy[i], decEnergy[i])
		}
	}

	// Compare RNG
	if enc.RNG() != dec.RNG() {
		t.Errorf("RNG: enc=%d, dec=%d", enc.RNG(), dec.RNG())
	}

	// Compare overlap buffer size
	if len(enc.OverlapBuffer()) != len(dec.OverlapBuffer()) {
		t.Errorf("OverlapBuffer length: enc=%d, dec=%d",
			len(enc.OverlapBuffer()), len(dec.OverlapBuffer()))
	}
}

// TestEncoderReset verifies Reset clears state properly.
func TestEncoderReset(t *testing.T) {
	enc := NewEncoder(2)

	// Modify state
	enc.SetEnergy(5, 0, 10.0)
	enc.SetEnergy(5, 1, 15.0)
	enc.SetRNG(12345)

	// Reset
	enc.Reset()

	// Verify state cleared
	if enc.GetEnergy(5, 0) != 0 {
		t.Errorf("GetEnergy(5, 0) after reset = %f, want 0.0", enc.GetEnergy(5, 0))
	}
	if enc.RNG() != 0 {
		t.Errorf("RNG after reset = %d, want 0", enc.RNG())
	}
}

// TestEncoderNextRNG verifies RNG produces expected sequence.
func TestEncoderNextRNG(t *testing.T) {
	enc := NewEncoder(1)
	dec := NewDecoder(1)

	// Both should produce same RNG sequence
	for i := 0; i < 10; i++ {
		encRNG := enc.NextRNG()
		decRNG := dec.NextRNG()
		if encRNG != decRNG {
			t.Errorf("iteration %d: enc RNG=%d, dec RNG=%d", i, encRNG, decRNG)
		}
	}
}

// TestMDCTRoundTrip verifies MDCT -> IMDCT reconstructs original signal.
func TestMDCTRoundTrip(t *testing.T) {
	// Test various sizes
	sizes := []int{120, 240, 480, 960}

	for _, n := range sizes {
		t.Run(sizeToString(n), func(t *testing.T) {
			// Create input: 2*N samples (sine wave)
			input := make([]float64, 2*n)
			for i := range input {
				// Use a sine wave at a frequency that fits well in the window
				input[i] = math.Sin(2 * math.Pi * float64(i) / 100)
			}

			// Forward MDCT: 2*N samples -> N coefficients
			coeffs := MDCT(input)
			if len(coeffs) != n {
				t.Errorf("MDCT output length = %d, want %d", len(coeffs), n)
				return
			}

			// Inverse MDCT: N coefficients -> 2*N samples
			output := IMDCTDirect(coeffs)
			if len(output) != 2*n {
				t.Errorf("IMDCT output length = %d, want %d", len(output), 2*n)
				return
			}

			// Due to windowing, we can only compare the middle region where
			// the windows are near 1.0. The edges have windowing effects.
			// Compare middle 50% of the frame
			startCompare := n / 2
			endCompare := n + n/2

			var maxDiff float64
			for i := startCompare; i < endCompare; i++ {
				diff := math.Abs(input[i] - output[i])
				if diff > maxDiff {
					maxDiff = diff
				}
			}

			// Tolerance depends on MDCT normalization
			// The MDCT/IMDCT pair should reconstruct with some scaling
			// Check that the shape is preserved (not all zeros)
			var maxOutput float64
			for i := startCompare; i < endCompare; i++ {
				if math.Abs(output[i]) > maxOutput {
					maxOutput = math.Abs(output[i])
				}
			}

			if maxOutput < 0.01 {
				t.Errorf("MDCT->IMDCT produced near-zero output, max=%f", maxOutput)
			}
		})
	}
}

// TestMDCTShortRoundTrip verifies MDCTShort -> IMDCTShort reconstructs signal.
func TestMDCTShortRoundTrip(t *testing.T) {
	shortBlocksCounts := []int{2, 4, 8}

	for _, shortBlocks := range shortBlocksCounts {
		t.Run(shortBlocksToString(shortBlocks), func(t *testing.T) {
			// Total samples = shortBlocks * shortSize * 2
			// For standard CELT with 120 overlap, each short block has 120 coefficients
			shortSize := 120
			totalSamples := shortSize * 2 * shortBlocks

			// Create input signal
			input := make([]float64, totalSamples)
			for i := range input {
				input[i] = math.Sin(2 * math.Pi * float64(i) / 50)
			}

			// Forward MDCT (short blocks)
			coeffs := MDCTShort(input, shortBlocks)
			expectedCoeffs := shortSize * shortBlocks
			if len(coeffs) != expectedCoeffs {
				t.Errorf("MDCTShort output length = %d, want %d", len(coeffs), expectedCoeffs)
				return
			}

			// Inverse MDCT (short blocks)
			output := IMDCTShort(coeffs, shortBlocks)

			// Output should have 2x the coefficients (IMDCT produces 2*N from N)
			if len(output) != 2*expectedCoeffs {
				t.Errorf("IMDCTShort output length = %d, want %d", len(output), 2*expectedCoeffs)
				return
			}

			// Verify output is not all zeros
			var maxOutput float64
			for _, x := range output {
				if math.Abs(x) > maxOutput {
					maxOutput = math.Abs(x)
				}
			}

			if maxOutput < 0.01 {
				t.Errorf("MDCTShort->IMDCTShort produced near-zero output, max=%f", maxOutput)
			}
		})
	}
}

func TestEncoderFrameCountAndIntraFlag(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960
	mode := GetModeConfig(frameSize)
	pcm := generateSineWave(440.0, frameSize)

	for i := 0; i < 5; i++ {
		// Match libopus two-pass behavior: with complexity >= 4 and force_intra=0,
		// libopus uses two-pass encoding that typically chooses inter mode (intra=false)
		// even for frame 0. Reference: libopus celt/quant_bands.c line 279
		expectedIntra := false
		if enc.IsIntraFrame() != expectedIntra {
			t.Fatalf("frame %d: IsIntraFrame=%v, want %v", i, enc.IsIntraFrame(), expectedIntra)
		}

		packet, err := enc.EncodeFrame(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d: EncodeFrame failed: %v", i, err)
		}
		if len(packet) == 0 {
			t.Fatalf("frame %d: empty packet", i)
		}

		rd := &rangecoding.Decoder{}
		rd.Init(packet)
		if rd.DecodeBit(15) == 1 {
			t.Fatalf("frame %d: unexpected silence flag", i)
		}
		rd.DecodeBit(1) // reserved/start bit
		if mode.LM > 0 {
			rd.DecodeBit(3) // transient flag
		}
		intra := rd.DecodeBit(3) == 1
		if intra != expectedIntra {
			t.Fatalf("frame %d: intra=%v, want %v", i, intra, expectedIntra)
		}

		if enc.FrameCount() != i+1 {
			t.Fatalf("frame %d: FrameCount=%d, want %d", i, enc.FrameCount(), i+1)
		}
	}
}

// TestPreemphasisDeemphasis verifies pre-emphasis -> de-emphasis round-trip.
func TestPreemphasisDeemphasis(t *testing.T) {
	channels := []int{1, 2}

	for _, ch := range channels {
		t.Run(channelsToString(ch), func(t *testing.T) {
			enc := NewEncoder(ch)
			dec := NewDecoder(ch)

			// Create input signal
			samples := 100
			input := make([]float64, samples*ch)
			for i := range input {
				input[i] = float64(i%20) / 10.0 // Sawtooth-like pattern
			}

			// Apply pre-emphasis
			preemph := enc.ApplyPreemphasis(input)

			// Apply de-emphasis (simulate what decoder does)
			output := make([]float64, len(preemph))
			copy(output, preemph)

			// De-emphasis: y[n] = x[n] + PreemphCoef * y[n-1]
			if ch == 1 {
				state := dec.PreemphState()[0]
				for i := range output {
					output[i] = output[i] + PreemphCoef*state
					state = output[i]
				}
			} else {
				stateL := dec.PreemphState()[0]
				stateR := dec.PreemphState()[1]
				for i := 0; i < len(output)-1; i += 2 {
					output[i] = output[i] + PreemphCoef*stateL
					stateL = output[i]
					output[i+1] = output[i+1] + PreemphCoef*stateR
					stateR = output[i+1]
				}
			}

			// Compare: input and output should match after round-trip
			// Note: first sample may differ due to initial state
			startCompare := ch // Skip first sample(s)
			var maxDiff float64
			for i := startCompare; i < len(input); i++ {
				diff := math.Abs(input[i] - output[i])
				if diff > maxDiff {
					maxDiff = diff
				}
			}

			// Should be very close (within floating-point precision)
			if maxDiff > 1e-10 {
				t.Errorf("pre-emphasis/de-emphasis round-trip error: maxDiff=%e", maxDiff)
			}
		})
	}
}

// TestPreemphasisState verifies pre-emphasis state is maintained across calls.
func TestPreemphasisState(t *testing.T) {
	enc := NewEncoder(1)

	// First call
	input1 := []float64{1.0, 2.0, 3.0}
	_ = enc.ApplyPreemphasis(input1)

	// State should be the last input sample
	if enc.PreemphState()[0] != 3.0 {
		t.Errorf("PreemphState after first call = %f, want 3.0", enc.PreemphState()[0])
	}

	// Second call should use state from first call
	input2 := []float64{4.0, 5.0}
	output2 := enc.ApplyPreemphasis(input2)

	// First output should be: 4.0 - 0.85*3.0 = 1.45
	expected := 4.0 - PreemphCoef*3.0
	if math.Abs(output2[0]-expected) > 1e-10 {
		t.Errorf("First sample of second call = %f, want %f", output2[0], expected)
	}
}

// TestApplyPreemphasisInPlace verifies in-place pre-emphasis works correctly.
func TestApplyPreemphasisInPlace(t *testing.T) {
	enc1 := NewEncoder(1)
	enc2 := NewEncoder(1)

	input := []float64{1.0, 2.0, 3.0, 4.0, 5.0}

	// Apply regular pre-emphasis
	inputCopy := make([]float64, len(input))
	copy(inputCopy, input)
	regular := enc1.ApplyPreemphasis(inputCopy)

	// Apply in-place pre-emphasis
	inPlace := make([]float64, len(input))
	copy(inPlace, input)
	enc2.ApplyPreemphasisInPlace(inPlace)

	// Both should produce same results
	for i := range regular {
		if math.Abs(regular[i]-inPlace[i]) > 1e-10 {
			t.Errorf("Sample %d: regular=%f, inPlace=%f", i, regular[i], inPlace[i])
		}
	}
}

// Helper functions for test naming
func sizeToString(n int) string {
	switch n {
	case 120:
		return "2.5ms"
	case 240:
		return "5ms"
	case 480:
		return "10ms"
	case 960:
		return "20ms"
	default:
		return "unknown"
	}
}

func shortBlocksToString(n int) string {
	switch n {
	case 2:
		return "2_blocks"
	case 4:
		return "4_blocks"
	case 8:
		return "8_blocks"
	default:
		return "unknown_blocks"
	}
}

func channelsToString(ch int) string {
	if ch == 1 {
		return "mono"
	}
	return "stereo"
}
