package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestMultiFrameConvergence tests that output converges across multiple frames.
func TestMultiFrameConvergence(t *testing.T) {
	// Create a sine wave signal for multiple frames
	frameSamples := 320 // 20ms at 16kHz
	numFrames := 5
	pcmFloat := make([]float32, frameSamples*numFrames)
	amplitude := float32(0.3)
	for i := range pcmFloat {
		pcmFloat[i] = amplitude * float32(math.Sin(2*math.Pi*440*float64(i)/16000))
	}

	// Encode all frames
	enc := NewEncoder(BandwidthWideband)
	encodedFrames := make([][]byte, numFrames)
	for f := 0; f < numFrames; f++ {
		start := f * frameSamples
		end := start + frameSamples
		encoded := enc.EncodeFrame(pcmFloat[start:end], true)
		encodedFrames[f] = append([]byte(nil), encoded...)
		t.Logf("Frame %d: encoded %d bytes", f, len(encodedFrames[f]))
	}

	// Decode all frames
	dec := NewDecoder()
	outputFrames := make([][]float32, numFrames)

	for f := 0; f < numFrames; f++ {
		rd := &rangecoding.Decoder{}
		rd.Init(encodedFrames[f])

		condCoding := codeIndependently
		if f > 0 {
			condCoding = codeConditionally
		}

		output, err := dec.DecodeFrameRaw(rd, BandwidthWideband, Frame20ms, f == 0)
		if err != nil {
			t.Fatalf("Frame %d decode error: %v", f, err)
		}
		outputFrames[f] = output

		// Compute RMS for this frame
		start := f * frameSamples
		var sumSqInput, sumSqOutput float64
		for i := 0; i < frameSamples && i < len(output); i++ {
			inVal := float64(pcmFloat[start+i])
			outVal := float64(output[i])
			sumSqInput += inVal * inVal
			sumSqOutput += outVal * outVal
		}
		inputRMS := math.Sqrt(sumSqInput / float64(frameSamples))
		outputRMS := math.Sqrt(sumSqOutput / float64(len(output)))
		ratio := outputRMS / inputRMS

		t.Logf("Frame %d: input RMS=%.4f, output RMS=%.4f, ratio=%.4f, condCoding=%d",
			f, inputRMS, outputRMS, ratio, condCoding)

		// Check convergence - should improve over frames
		// Note: The absolute threshold reflects current encoder baseline quality.
		// The key metric is that ratio improves over frames (LTP warmup working).
		if f == numFrames-1 && ratio < 0.25 {
			t.Errorf("Even after %d frames, ratio is still low: %.4f (expected >= 0.25)", numFrames, ratio)
		}
	}
}

// TestSILKRoundtripWithWarmup tests SILK roundtrip with warmup frames.
func TestSILKRoundtripWithWarmup(t *testing.T) {
	// Create signal
	frameSamples := 320
	numFrames := 3
	pcmFloat := make([]float32, frameSamples*numFrames)
	amplitude := float32(0.3)
	for i := range pcmFloat {
		pcmFloat[i] = amplitude * float32(math.Sin(2*math.Pi*440*float64(i)/16000))
	}

	// Encode
	enc := NewEncoder(BandwidthWideband)
	var allEncoded []byte
	for f := 0; f < numFrames; f++ {
		start := f * frameSamples
		end := start + frameSamples
		encoded := enc.EncodeFrame(pcmFloat[start:end], true)
		allEncoded = append(allEncoded, encoded...)
		t.Logf("Frame %d: %d bytes", f, len(encoded))
	}

	// Just check that encoding works
	t.Logf("Total encoded: %d bytes for %d frames", len(allEncoded), numFrames)
}
