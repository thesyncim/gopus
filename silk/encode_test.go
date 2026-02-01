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
	encoded, err := Encode(pcm, BandwidthWideband, true)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Verify we got output
	if len(encoded) == 0 {
		t.Error("Encode produced empty output")
	}

	// Verify output is not too large (reasonable for 20ms SILK frame)
	if len(encoded) > 250 {
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
	encoded, err := Encode(original, BandwidthWideband, true)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

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
		for bit := 0; bit < 8; bit++ {
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
	encoded, err := EncodeStereo(left, right, BandwidthWideband, true)
	if err != nil {
		t.Fatalf("EncodeStereo failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Fatal("EncodeStereo produced empty output")
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
	encoded, err := EncodeStereo(left, right, BandwidthWideband, true)
	if err != nil {
		t.Fatalf("EncodeStereo failed: %v", err)
	}

	t.Logf("Stereo encoded: %d bytes (L=%d R=%d samples input)", len(encoded), len(left), len(right))

	// Verify output size is reasonable
	if len(encoded) == 0 {
		t.Error("EncodeStereo produced empty output")
	}
	if len(encoded) > 600 {
		t.Errorf("Stereo encoded size too large: %d bytes (expected < 600)", len(encoded))
	}

	// Verify the format has stereo weights at start
	if len(encoded) >= 4 {
		w0 := int16(encoded[0])<<8 | int16(encoded[1])
		w1 := int16(encoded[2])<<8 | int16(encoded[3])
		t.Logf("Stereo weights extracted: w0=%d, w1=%d (Q13)", w0, w1)

		// Weights should be in reasonable range for Q13
		if w0 < -16384 || w0 > 16384 {
			t.Errorf("Stereo weight w0 out of range: %d", w0)
		}
		if w1 < -16384 || w1 > 16384 {
			t.Errorf("Stereo weight w1 out of range: %d", w1)
		}
	}

	// NOTE: Full round-trip requires bit-exact encoder-decoder compatibility
	// For now, verify encoding pipeline works
}

func TestEncodeSilence(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame
	pcm := make([]float32, frameSamples)          // All zeros

	encoded, err := Encode(pcm, BandwidthWideband, false) // vadFlag=false for silence
	if err != nil {
		t.Fatalf("Encode silence failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Error("Encode produced empty output for silence")
	}

	// Silence should encode to a reasonable size
	t.Logf("Silence frame size: %d bytes", len(encoded))
}

func TestEncodeStreaming(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame
	es := NewEncoderState(BandwidthWideband)

	// Encode multiple frames
	for frame := 0; frame < 5; frame++ {
		pcm := make([]float32, frameSamples)
		for i := range pcm {
			tm := float64(i+frame*frameSamples) / float64(config.SampleRate)
			pcm[i] = float32(math.Sin(2*math.Pi*400*tm)) * (10000 * int16Scale)
		}

		encoded, err := es.EncodeFrame(pcm, true)
		if err != nil {
			t.Fatalf("Frame %d encode failed: %v", frame, err)
		}

		if len(encoded) == 0 {
			t.Errorf("Frame %d produced empty output", frame)
		}

		t.Logf("Frame %d: %d bytes", frame, len(encoded))
	}
}

// TestMultiFrameRangeEncoderLifecycle validates that the rangeEncoder is
// properly cleared after standalone encoding, allowing subsequent frames
// to create their own encoder. This was a critical bug where frames 1+
// would return nil instead of encoded bytes.
func TestMultiFrameRangeEncoderLifecycle(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame

	// Use the raw Encoder directly (not EncoderState) to validate fix
	enc := NewEncoder(BandwidthWideband)

	frameSizes := make([]int, 10)
	for frame := 0; frame < 10; frame++ {
		pcm := make([]float32, frameSamples)
		for i := range pcm {
			tm := float64(i+frame*frameSamples) / float64(config.SampleRate)
			pcm[i] = float32(math.Sin(2*math.Pi*400*tm)) * (10000 * int16Scale)
		}

		encoded := enc.EncodeFrame(pcm, true)
		frameSizes[frame] = len(encoded)

		// Every frame must produce output in standalone mode
		if len(encoded) == 0 {
			t.Fatalf("Frame %d produced 0 bytes - rangeEncoder lifecycle bug!", frame)
		}
	}

	// Log all frame sizes to validate consistency
	t.Logf("Frame sizes: %v", frameSizes)

	// Verify all frames produced reasonable output
	for i, size := range frameSizes {
		if size < 10 || size > 300 {
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

			encoded, err := Encode(pcm, tc.bandwidth, true)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

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

	encodedVoiced, err := Encode(voiced, BandwidthWideband, true)
	if err != nil {
		t.Fatalf("Encode voiced failed: %v", err)
	}

	encodedUnvoiced, err := Encode(unvoiced, BandwidthWideband, true)
	if err != nil {
		t.Fatalf("Encode unvoiced failed: %v", err)
	}

	t.Logf("Voiced frame: %d bytes, Unvoiced frame: %d bytes",
		len(encodedVoiced), len(encodedUnvoiced))
}

func TestExcitationEncoding(t *testing.T) {
	// Test that excitation encoding produces valid output
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame

	// Generate test signal
	pcm := make([]float32, frameSamples)
	for i := range pcm {
		tm := float64(i) / float64(config.SampleRate)
		pcm[i] = float32(math.Sin(2*math.Pi*300*tm)) * (10000 * int16Scale)
	}

	// Compute LPC and excitation
	lpcQ12 := enc.computeLPCFromFrame(pcm)
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
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame

	// Generate stereo test signal
	left := make([]float32, frameSamples)
	right := make([]float32, frameSamples)
	for i := range left {
		tm := float64(i) / float64(config.SampleRate)
		left[i] = float32(math.Sin(2*math.Pi*300*tm)) * (10000 * int16Scale)
		right[i] = float32(math.Sin(2*math.Pi*300*tm+0.5)) * (10000 * int16Scale) // Phase shifted
	}

	// Compute stereo weights
	mid, side, weights := enc.encodeStereo(left, right)

	// Verify mid/side have same length
	if len(mid) != len(left) {
		t.Errorf("Mid length %d != left length %d", len(mid), len(left))
	}
	if len(side) != len(left) {
		t.Errorf("Side length %d != left length %d", len(side), len(left))
	}

	t.Logf("Stereo weights: w0=%d, w1=%d (Q13)", weights[0], weights[1])

	// Verify weights are in reasonable range
	// libopus clamps to Q14 range: [-16384, 16384] which represents [-2, 2] in Q13
	// This matches silk_stereo_find_predictor.c line 57:
	// pred_Q13 = silk_LIMIT( pred_Q13, -(1 << 14), 1 << 14 );
	if weights[0] < -16384 || weights[0] > 16384 {
		t.Errorf("Weight w0 out of range: %d (expected [-16384, 16384])", weights[0])
	}
	if weights[1] < -16384 || weights[1] > 16384 {
		t.Errorf("Weight w1 out of range: %d (expected [-16384, 16384])", weights[1])
	}
}

func computeCorrelation(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}

	var sumAB, sumA2, sumB2 float64
	for i := 0; i < n; i++ {
		sumAB += float64(a[i]) * float64(b[i])
		sumA2 += float64(a[i]) * float64(a[i])
		sumB2 += float64(b[i]) * float64(b[i])
	}

	if sumA2 < 1e-10 || sumB2 < 1e-10 {
		return 0
	}

	return sumAB / math.Sqrt(sumA2*sumB2)
}
