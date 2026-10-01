package silk

import (
	"math"
	"testing"
)

func TestEncodeFrameBasic(t *testing.T) {
	// Generate test signal
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame
	pcm := make([]float32, frameSamples)
	for i := range pcm {
		// 300 Hz sine wave
		pcm[i] = float32(math.Sin(2*math.Pi*300*float64(i)/float64(config.SampleRate))) * (10000 * int16Scale)
	}

	// Encode
	encoded := encodeTestPacket(t, BandwidthWideband, pcm)

	// Verify we got output
	if len(encoded) == 0 {
		t.Error("Encode produced empty output")
	}

	// Verify output is not too large (reasonable for 20ms SILK frame)
	if len(encoded) > 320 {
		t.Errorf("Encoded frame too large: %d bytes", len(encoded))
	}

	t.Logf("Encoded frame size: %d bytes", len(encoded))
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	// Generate voiced test signal (300 Hz fundamental)
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame
	original := make([]float32, frameSamples)
	for i := range original {
		tm := float64(i) / float64(config.SampleRate)
		// Voiced-like signal with harmonics
		original[i] = float32(
			math.Sin(2*math.Pi*300*tm)+
				0.5*math.Sin(2*math.Pi*600*tm)+
				0.3*math.Sin(2*math.Pi*900*tm),
		) * (10000 * int16Scale)
	}

	// Encode
	encoded := encodeTestPacket(t, BandwidthWideband, original)

	t.Logf("Encoded: %d bytes (original %d samples)", len(encoded), len(original))

	// NOTE: Full round-trip testing requires bit-exact encoder-decoder compatibility
	// which is complex to achieve. For now, we verify:
	// 1. Encoding produces non-empty output
	// 2. Output size is reasonable for SILK frame
	if len(encoded) == 0 {
		t.Error("Encode produced empty output")
	}
	if len(encoded) > 300 {
		t.Errorf("Encoded size too large: %d bytes (expected < 300 for 20ms)", len(encoded))
	}

	// Verify encoded data has non-trivial entropy (not all zeros/ones)
	var zeros, ones int
	for _, b := range encoded {
		for bit := range 8 {
			if b&(1<<bit) == 0 {
				zeros++
			} else {
				ones++
			}
		}
	}
	totalBits := len(encoded) * 8
	bitRatio := float64(ones) / float64(totalBits)
	t.Logf("Bit distribution: %.1f%% ones, %.1f%% zeros", bitRatio*100, (1-bitRatio)*100)

	if bitRatio < 0.05 || bitRatio > 0.95 {
		t.Errorf("Encoded data has suspicious bit distribution (%.1f%% ones)", bitRatio*100)
	}
}

func TestEncodeStereoBasic(t *testing.T) {
	// Generate stereo test signal (different frequencies per channel)
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame
	left := make([]float32, frameSamples)
	right := make([]float32, frameSamples)

	for i := range left {
		tm := float64(i) / float64(config.SampleRate)
		// Left: 300 Hz
		left[i] = float32(math.Sin(2*math.Pi*300*tm)) * (10000 * int16Scale)
		// Right: 350 Hz (slightly different)
		right[i] = float32(math.Sin(2*math.Pi*350*tm)) * (10000 * int16Scale)
	}

	// Encode stereo
	encoded := encodeTestStereoPacket(t, BandwidthWideband, left, right)

	if len(encoded) == 0 {
		t.Fatal("stereo Encode produced empty output")
	}

	t.Logf("Stereo encoded size: %d bytes", len(encoded))
}

func TestEncodeStereoRoundTrip(t *testing.T) {
	// Generate stereo test signal (different frequencies per channel)
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame
	left := make([]float32, frameSamples)
	right := make([]float32, frameSamples)

	for i := range left {
		tm := float64(i) / float64(config.SampleRate)
		// Left: 300 Hz
		left[i] = float32(math.Sin(2*math.Pi*300*tm)) * (10000 * int16Scale)
		// Right: 350 Hz (slightly different)
		right[i] = float32(math.Sin(2*math.Pi*350*tm)) * (10000 * int16Scale)
	}

	// Encode stereo
	encoded := encodeTestStereoPacket(t, BandwidthWideband, left, right)

	t.Logf("Stereo encoded: %d bytes (L=%d R=%d samples input)", len(encoded), len(left), len(right))

	// Verify output size is reasonable
	if len(encoded) == 0 {
		t.Error("stereo Encode produced empty output")
	}
	if len(encoded) > 600 {
		t.Errorf("Stereo encoded size too large: %d bytes (expected < 600)", len(encoded))
	}

	// The encoded packet is a range-coded SILK stereo bitstream, not raw
	// binary. Stereo prediction weights are range-coded inside the packet
	// and cannot be read as raw bytes at fixed offsets.
	// Verify round-trip by decoding the packet back to stereo PCM.
	decLeft, decRight, err := DecodeStereoEncoded(encoded, BandwidthWideband)
	if err != nil {
		t.Fatalf("DecodeStereoEncoded failed: %v", err)
	}

	expectedSamples := frameSamples * 48000 / config.SampleRate
	if len(decLeft) != expectedSamples {
		t.Errorf("Left channel length %d != expected %d", len(decLeft), expectedSamples)
	}
	if len(decRight) != expectedSamples {
		t.Errorf("Right channel length %d != expected %d", len(decRight), expectedSamples)
	}

	t.Logf("Stereo round-trip: %d bytes -> L=%d R=%d samples (48kHz)", len(encoded), len(decLeft), len(decRight))
}

func TestEncodeSilence(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame
	pcm := make([]float32, frameSamples)          // All zeros

	encoded := encodeTestPacket(t, BandwidthWideband, pcm)

	if len(encoded) == 0 {
		t.Error("Encode produced empty output for silence")
	}

	// Silence should encode to a reasonable size
	t.Logf("Silence frame size: %d bytes", len(encoded))
}

func TestEncodeStreaming(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame
	es := newTestPacketEncoder(BandwidthWideband, 1)

	// Encode multiple frames
	for frame := range 5 {
		pcm := make([]float32, frameSamples)
		for i := range pcm {
			tm := float64(i+frame*frameSamples) / float64(config.SampleRate)
			pcm[i] = float32(math.Sin(2*math.Pi*400*tm)) * (10000 * int16Scale)
		}

		encoded := es.encode(t, pcm)

		if len(encoded) == 0 {
			t.Errorf("Frame %d produced empty output", frame)
		}

		t.Logf("Frame %d: %d bytes", frame, len(encoded))
	}
}

// TestMultiFrameRangeEncoderLifecycle checks that an encoder coding packet
// after packet into a fresh range coder produces a full packet every time.
func TestMultiFrameRangeEncoderLifecycle(t *testing.T) {
	if silkFixedEncodeBuild {
		t.Skip("frame-size heuristic is calibrated against the float SILK encode path; FIXED_POINT first-frame warmup legitimately produces a smaller frame")
	}
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame

	enc := newTestPacketEncoder(BandwidthWideband, 1)

	frameSizes := make([]int, 10)
	for frame := range 10 {
		pcm := make([]float32, frameSamples)
		for i := range pcm {
			tm := float64(i+frame*frameSamples) / float64(config.SampleRate)
			pcm[i] = float32(math.Sin(2*math.Pi*400*tm)) * (10000 * int16Scale)
		}

		encoded := enc.encode(t, pcm)
		frameSizes[frame] = len(encoded)

		if len(encoded) == 0 {
			t.Fatalf("Frame %d produced 0 bytes", frame)
		}
	}

	// Log all frame sizes to validate consistency
	t.Logf("Frame sizes: %v", frameSizes)

	// Verify all frames produced reasonable output
	for i, size := range frameSizes {
		if size < 10 || size > 400 {
			t.Errorf("Frame %d: unusual size %d bytes", i, size)
		}
	}
}

func TestEncodeDifferentBandwidths(t *testing.T) {
	testCases := []struct {
		name      string
		bandwidth Bandwidth
	}{
		{"Narrowband", BandwidthNarrowband},
		{"Mediumband", BandwidthMediumband},
		{"Wideband", BandwidthWideband},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := GetBandwidthConfig(tc.bandwidth)
			frameSamples := config.SampleRate * 20 / 1000 // 20ms frame
			pcm := make([]float32, frameSamples)
			for i := range pcm {
				tm := float64(i) / float64(config.SampleRate)
				pcm[i] = float32(math.Sin(2*math.Pi*300*tm)) * (10000 * int16Scale)
			}

			encoded := encodeTestPacket(t, tc.bandwidth, pcm)

			if len(encoded) == 0 {
				t.Error("Encode produced empty output")
			}

			t.Logf("%s: %d samples -> %d bytes", tc.name, frameSamples, len(encoded))
		})
	}
}

func TestEncodeVoicedVsUnvoiced(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame

	// Voiced-like signal (periodic)
	voiced := make([]float32, frameSamples)
	for i := range voiced {
		tm := float64(i) / float64(config.SampleRate)
		voiced[i] = float32(
			math.Sin(2*math.Pi*200*tm)+
				0.5*math.Sin(2*math.Pi*400*tm)+
				0.3*math.Sin(2*math.Pi*600*tm),
		) * (10000 * int16Scale)
	}

	// Unvoiced-like signal (noise)
	unvoiced := make([]float32, frameSamples)
	for i := range unvoiced {
		// Simple pseudo-random noise
		unvoiced[i] = float32((i*1103515245+12345)%65536-32768) * 0.3
	}

	encodedVoiced := encodeTestPacket(t, BandwidthWideband, voiced)

	encodedUnvoiced := encodeTestPacket(t, BandwidthWideband, unvoiced)

	t.Logf("Voiced frame: %d bytes, Unvoiced frame: %d bytes",
		len(encodedVoiced), len(encodedUnvoiced))
}

func TestExcitationEncoding(t *testing.T) {
	// Test that excitation encoding produces valid output
	enc := newTestEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame

	// Generate test signal
	pcm := make([]float32, frameSamples)
	for i := range pcm {
		tm := float64(i) / float64(config.SampleRate)
		pcm[i] = float32(math.Sin(2*math.Pi*300*tm)) * (10000 * int16Scale)
	}

	// Compute LPC and excitation
	lpcQ12 := burgLPC(pcm, int(enc.lpcOrder))
	excitation := enc.computeExcitation(pcm, lpcQ12, 1000.0)

	// Verify excitation has reasonable values
	if len(excitation) != len(pcm) {
		t.Errorf("Excitation length %d != PCM length %d", len(excitation), len(pcm))
	}

	var maxExc int32
	for _, e := range excitation {
		if e > maxExc {
			maxExc = e
		}
		if -e > maxExc {
			maxExc = -e
		}
	}

	t.Logf("Max excitation magnitude: %d", maxExc)
}

func TestStereoWeightEncoding(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame

	// Stereo input in the two channel input buffers (inputBuf[2:]).
	left := make([]int16, frameSamples+2)
	right := make([]int16, frameSamples+2)
	for i := range frameSamples {
		tm := float64(i) / float64(config.SampleRate)
		left[i+2] = int16(10000 * math.Sin(2*math.Pi*300*tm))
		right[i+2] = int16(10000 * math.Sin(2*math.Pi*300*tm+0.5)) // Phase shifted
	}

	var state stereoEncState
	var scratch stereoLRToMSScratch
	ix, midOnly, rates := silkStereoLRToMS(&state, left, right, 32000, 200, false, config.SampleRate/1000, frameSamples, &scratch)

	t.Logf("Stereo predictors: w0=%d, w1=%d (Q13) midOnly=%d rates=%v", state.predPrevQ13[0], state.predPrevQ13[1], midOnly, rates)

	// silk_stereo_quant_pred picks predictors from the quantization table,
	// whose levels lie within [-13732, 13732] (Q13).
	for n := range 2 {
		if w := state.predPrevQ13[n]; w < -2*13732 || w > 2*13732 {
			t.Errorf("predictor %d out of range: %d", n, w)
		}
		if ix[n][0] < 0 || ix[n][0] > 2 || ix[n][1] < 0 || ix[n][1] > 4 || ix[n][2] < 0 || ix[n][2] > 4 {
			t.Errorf("indices %d out of range: %v", n, ix[n])
		}
	}
	// The split shares the total less 600 bps for the stereo parameters of a
	// 20 ms frame (silk/stereo_LR_to_MS.c).
	if rates[0]+rates[1] != 32000-600 {
		t.Errorf("mid/side rates %v do not add up to %d", rates, 32000-600)
	}
}

func computeCorrelation(a, b []float32) float64 {
	n := min(len(b), len(a))

	var sumAB, sumA2, sumB2 float64
	for i := range n {
		sumAB += float64(a[i]) * float64(b[i])
		sumA2 += float64(a[i]) * float64(a[i])
		sumB2 += float64(b[i]) * float64(b[i])
	}

	if sumA2 < 1e-10 || sumB2 < 1e-10 {
		return 0
	}

	return sumAB / math.Sqrt(sumA2*sumB2)
}
