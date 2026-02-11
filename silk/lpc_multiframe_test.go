package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestLPCPredictionMultiFrame tests that LPC prediction builds up over multiple frames.
// The key hypothesis: First frame has zero history, so LPC prediction is near-zero.
// Subsequent frames should have proper history and much better amplitude recovery.
func TestLPCPredictionMultiFrame(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms = 320 samples at 16kHz

	// Generate a voiced-like signal (sine wave at 300 Hz)
	amplitude := float32(0.3)
	numFrames := 5

	// Create multiple frames of input
	allPCM := make([]float32, frameSamples*numFrames)
	for i := range allPCM {
		tm := float64(i) / float64(config.SampleRate)
		allPCM[i] = amplitude * float32(math.Sin(2*math.Pi*300*tm))
	}

	// Compute input RMS
	var inputSumSq float64
	for _, s := range allPCM[:frameSamples] {
		inputSumSq += float64(s) * float64(s)
	}
	inputRMS := math.Sqrt(inputSumSq / float64(frameSamples))

	t.Logf("=== Multi-frame LPC test ===")
	t.Logf("Sample rate: %d Hz", config.SampleRate)
	t.Logf("Frame samples: %d", frameSamples)
	t.Logf("Num frames: %d", numFrames)
	t.Logf("Input amplitude: %.3f, Input RMS: %.4f", amplitude, inputRMS)

	// Create a persistent decoder
	decoder := NewDecoder()

	// Process each frame separately
	for frame := 0; frame < numFrames; frame++ {
		pcm := allPCM[frame*frameSamples : (frame+1)*frameSamples]

		// Encode this frame
		encoded, err := Encode(pcm, BandwidthWideband, true)
		if err != nil {
			t.Fatalf("Frame %d: Encode failed: %v", frame, err)
		}

		// Decode using the same decoder (state persists!)
		var rd rangecoding.Decoder
		rd.Init(encoded)
		decoded, err := decoder.DecodeFrame(&rd, BandwidthWideband, Frame20ms, true)
		if err != nil {
			t.Fatalf("Frame %d: Decode failed: %v", frame, err)
		}

		// Compute output RMS for this frame
		var outputSumSq float64
		for _, s := range decoded {
			outputSumSq += float64(s) * float64(s)
		}
		outputRMS := math.Sqrt(outputSumSq / float64(len(decoded)))

		ratio := outputRMS / inputRMS

		// Get decoder state info
		st := &decoder.state[0]

		// Check sLPCQ14Buf - is there history now?
		hasHistory := false
		maxHistory := int32(0)
		for i := 0; i < 16; i++ {
			if st.sLPCQ14Buf[i] != 0 {
				hasHistory = true
				if st.sLPCQ14Buf[i] > maxHistory {
					maxHistory = st.sLPCQ14Buf[i]
				}
				if -st.sLPCQ14Buf[i] > maxHistory {
					maxHistory = -st.sLPCQ14Buf[i]
				}
			}
		}

		t.Logf("Frame %d: Output RMS=%.4f, Ratio=%.4f, HasHistory=%v, MaxHistory=%d",
			frame, outputRMS, ratio, hasHistory, maxHistory)

		// Log first few samples
		if frame < 3 {
			t.Logf("  First 5 decoded: %.4f, %.4f, %.4f, %.4f, %.4f",
				decoded[0], decoded[1], decoded[2], decoded[3], decoded[4])
		}
	}

	// The key test: does output improve across frames?
	// Frame 0 might be ~6% due to no history
	// Frame 1+ should be much better as LPC builds up
}

// TestLPCHistoryPreservation tests that sLPCQ14Buf is properly preserved between subframes.
func TestLPCHistoryPreservation(t *testing.T) {
	config := GetBandwidthConfig(BandwidthNarrowband)
	frameSamples := config.SampleRate * 20 / 1000 // 160 samples at 8kHz

	// Create a constant amplitude signal
	amplitude := float32(0.5)
	pcm := make([]float32, frameSamples)
	for i := range pcm {
		pcm[i] = amplitude
	}

	// Encode
	encoded, err := Encode(pcm, BandwidthNarrowband, true)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Create decoder
	decoder := NewDecoder()

	// Decode with tracing
	var rd rangecoding.Decoder
	rd.Init(encoded)

	// We want to trace the sLPC values during decode
	// Let's decode and check the state after
	decoded, err := decoder.DecodeFrame(&rd, BandwidthNarrowband, Frame20ms, true)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	st := &decoder.state[0]

	t.Logf("=== After first frame ===")
	t.Logf("Decoded samples: %d", len(decoded))
	t.Logf("nbSubfr: %d", st.nbSubfr)
	t.Logf("subfrLength: %d", st.subfrLength)
	t.Logf("frameLength: %d", st.frameLength)

	// Check the LPC history buffer
	t.Logf("sLPCQ14Buf after decode:")
	for i := 0; i < 16; i++ {
		if st.sLPCQ14Buf[i] != 0 {
			t.Logf("  sLPCQ14Buf[%d] = %d (%.4f)", i, st.sLPCQ14Buf[i], float64(st.sLPCQ14Buf[i])/16384.0)
		}
	}

	// Log output statistics
	var maxOutput, sumOutput float64
	for _, s := range decoded {
		absS := math.Abs(float64(s))
		if absS > maxOutput {
			maxOutput = absS
		}
		sumOutput += float64(s)
	}
	t.Logf("Output max: %.4f, mean: %.4f", maxOutput, sumOutput/float64(len(decoded)))

	// Now decode a second frame to see if history helps
	t.Logf("\n=== Decoding second frame ===")

	// Encode another frame
	encoded2, err := Encode(pcm, BandwidthNarrowband, true)
	if err != nil {
		t.Fatalf("Encode2 failed: %v", err)
	}

	var rd2 rangecoding.Decoder
	rd2.Init(encoded2)
	decoded2, err := decoder.DecodeFrame(&rd2, BandwidthNarrowband, Frame20ms, true)
	if err != nil {
		t.Fatalf("Decode2 failed: %v", err)
	}

	// Check output
	var maxOutput2, sumOutput2 float64
	for _, s := range decoded2 {
		absS := math.Abs(float64(s))
		if absS > maxOutput2 {
			maxOutput2 = absS
		}
		sumOutput2 += float64(s)
	}
	t.Logf("Output2 max: %.4f, mean: %.4f", maxOutput2, sumOutput2/float64(len(decoded2)))

	// Compare
	t.Logf("\nAmplitude improvement: %.2fx", maxOutput2/maxOutput)
}

// TestWithinFrameLPCBuildup tests if LPC prediction builds up WITHIN a single frame.
// In libopus, the LPC synthesis happens subframe by subframe, with each subframe's
// output becoming the next subframe's history.
func TestWithinFrameLPCBuildup(t *testing.T) {
	config := GetBandwidthConfig(BandwidthNarrowband)
	frameSamples := config.SampleRate * 20 / 1000 // 160 samples at 8kHz

	// Create a constant amplitude signal
	amplitude := float32(0.5)
	pcm := make([]float32, frameSamples)
	for i := range pcm {
		pcm[i] = amplitude
	}

	// Encode
	encoded, err := Encode(pcm, BandwidthNarrowband, true)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Decode
	decoder := NewDecoder()
	var rd rangecoding.Decoder
	rd.Init(encoded)
	decoded, err := decoder.DecodeFrame(&rd, BandwidthNarrowband, Frame20ms, true)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	st := &decoder.state[0]
	subfrLength := st.subfrLength
	nbSubfr := st.nbSubfr

	t.Logf("=== Within-frame LPC buildup ===")
	t.Logf("Subframe length: %d, Num subframes: %d", subfrLength, nbSubfr)

	// Compute RMS for each subframe
	for k := 0; k < nbSubfr; k++ {
		start := k * subfrLength
		end := start + subfrLength
		if end > len(decoded) {
			end = len(decoded)
		}

		var sumSq float64
		for i := start; i < end; i++ {
			sumSq += float64(decoded[i]) * float64(decoded[i])
		}
		rms := math.Sqrt(sumSq / float64(end-start))

		// Also get first/last few samples of this subframe
		t.Logf("Subframe %d: RMS=%.4f, first=%.4f, last=%.4f",
			k, rms, decoded[start], decoded[end-1])
	}

	// The hypothesis is that subframe 0 has low output, but subframes 1-3 improve
	// as the LPC prediction history builds up within the frame.
}
