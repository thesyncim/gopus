package testvectors

import (
	"math"
	"testing"
)

func TestComputeQuality_IdenticalSignals(t *testing.T) {
	// Identical signals should produce very high Q value
	samples := make([]int16, 1000)
	for i := range samples {
		samples[i] = int16(1000 * math.Sin(float64(i)*0.1))
	}

	q := ComputeQuality(samples, samples, 48000)

	if q < 90 {
		t.Errorf("identical signals should have Q > 90, got Q=%.2f", q)
	}

	t.Logf("Identical signals: Q=%.2f", q)
}

func TestComputeQuality_KnownNoise(t *testing.T) {
	// Create reference signal
	n := 10000
	reference := make([]int16, n)
	for i := range reference {
		reference[i] = int16(10000 * math.Sin(float64(i)*0.05))
	}

	// Add known noise level
	// For -20dB noise (noise power = signal power / 100)
	// We want SNR = 20dB, which should give Q = (20 - 48) * (100/48) = -58.3
	decoded := make([]int16, n)
	noiseLevel := 0.1 // 10% noise = -20dB approximately
	for i := range decoded {
		noise := noiseLevel * float64(reference[i])
		decoded[i] = int16(float64(reference[i]) + noise)
	}

	q := ComputeQuality(decoded, reference, 48000)

	// With 10% noise, actual SNR is about 20dB
	// Q = (20 - 48) * (100/48) = -58.3
	// Allow some tolerance due to rounding
	expectedQ := (20.0 - TargetSNR) * QualityScale
	tolerance := 5.0

	if math.Abs(q-expectedQ) > tolerance {
		t.Errorf("expected Q around %.1f (+/- %.1f), got %.2f", expectedQ, tolerance, q)
	}

	t.Logf("Known noise (10%%): Q=%.2f (expected ~%.1f)", q, expectedQ)
}

func TestComputeQuality_HighSNR(t *testing.T) {
	// Create reference signal
	n := 10000
	reference := make([]int16, n)
	for i := range reference {
		reference[i] = int16(20000 * math.Sin(float64(i)*0.05))
	}

	// Add very small noise (should give high SNR)
	decoded := make([]int16, n)
	noiseLevel := 0.001 // 0.1% noise = ~60dB SNR
	for i := range decoded {
		noise := noiseLevel * float64(reference[i])
		decoded[i] = int16(float64(reference[i]) + noise)
	}

	q := ComputeQuality(decoded, reference, 48000)

	// Should be well above threshold
	if !QualityPasses(q) {
		t.Errorf("high SNR signal should pass quality threshold, got Q=%.2f", q)
	}

	t.Logf("High SNR (0.1%% noise): Q=%.2f", q)
}

func TestComputeQuality_SilentReference(t *testing.T) {
	reference := make([]int16, 1000) // All zeros
	decoded := make([]int16, 1000)
	for i := range decoded {
		decoded[i] = 100 // Some noise
	}

	q := ComputeQuality(decoded, reference, 48000)

	// Noise against silence should be very bad
	if !math.IsInf(q, -1) {
		t.Errorf("noise against silent reference should be -Inf, got Q=%.2f", q)
	}
}

func TestComputeQuality_BothSilent(t *testing.T) {
	reference := make([]int16, 1000) // All zeros
	decoded := make([]int16, 1000)   // All zeros

	q := ComputeQuality(decoded, reference, 48000)

	// Both silent = perfect match
	if q != 100.0 {
		t.Errorf("both silent should give Q=100, got Q=%.2f", q)
	}
}

func TestComputeQuality_LengthMismatch(t *testing.T) {
	// Decoder produces fewer samples - should use shorter length
	reference := make([]int16, 1000)
	decoded := make([]int16, 500)

	for i := range reference {
		reference[i] = int16(1000 * math.Sin(float64(i)*0.1))
	}
	for i := range decoded {
		decoded[i] = reference[i] // Identical for the overlap
	}

	q := ComputeQuality(decoded, reference, 48000)

	// Should still compute valid Q for overlapping region
	if math.IsNaN(q) || math.IsInf(q, -1) {
		t.Errorf("length mismatch should still compute valid Q, got Q=%.2f", q)
	}

	// Identical samples should give high Q
	if q < 90 {
		t.Errorf("identical overlapping samples should have high Q, got Q=%.2f", q)
	}

	t.Logf("Length mismatch (500 vs 1000): Q=%.2f", q)
}

func TestComputeQuality_EmptySlices(t *testing.T) {
	q1 := ComputeQuality([]int16{}, []int16{1, 2, 3}, 48000)
	q2 := ComputeQuality([]int16{1, 2, 3}, []int16{}, 48000)

	if !math.IsInf(q1, -1) {
		t.Errorf("empty decoded should give -Inf, got Q=%.2f", q1)
	}
	if !math.IsInf(q2, -1) {
		t.Errorf("empty reference should give -Inf, got Q=%.2f", q2)
	}
}

func TestQualityPasses(t *testing.T) {
	testCases := []struct {
		q      float64
		passes bool
	}{
		{100.0, true},
		{50.0, true},
		{1.0, true},
		{0.0, true},   // Threshold
		{-0.1, false}, // Just below
		{-50.0, false},
		{math.Inf(-1), false},
	}

	for _, tc := range testCases {
		result := QualityPasses(tc.q)
		if result != tc.passes {
			t.Errorf("QualityPasses(%.2f): expected %v, got %v", tc.q, tc.passes, result)
		}
	}
}

func TestCompareSamples(t *testing.T) {
	// Identical samples should have MSE = 0
	a := []int16{100, 200, 300, 400}
	mse := CompareSamples(a, a)
	if mse != 0 {
		t.Errorf("identical samples should have MSE=0, got %f", mse)
	}

	// Known difference
	b := []int16{110, 210, 310, 410} // +10 each
	mse = CompareSamples(a, b)
	expected := float64(10*10) // MSE = 100
	if mse != expected {
		t.Errorf("expected MSE=%.1f, got %.1f", expected, mse)
	}
}

func TestNormalizedSNR(t *testing.T) {
	// Signal with known power
	signal := make([]int16, 1000)
	for i := range signal {
		signal[i] = 1000 // Constant signal, power = 1000^2 = 1e6
	}

	// Noise with known power
	noise := make([]int16, 1000)
	for i := range noise {
		noise[i] = 100 // Constant noise, power = 100^2 = 1e4
	}

	snr := NormalizedSNR(signal, noise)
	// Expected: 10 * log10(1e6 / 1e4) = 10 * log10(100) = 20 dB
	expected := 20.0

	if math.Abs(snr-expected) > 0.01 {
		t.Errorf("expected SNR=%.1f dB, got %.1f dB", expected, snr)
	}
}

func TestNormalizedSNR_ZeroNoise(t *testing.T) {
	signal := []int16{100, 200, 300}
	noise := []int16{0, 0, 0}

	snr := NormalizedSNR(signal, noise)

	if !math.IsInf(snr, 1) {
		t.Errorf("zero noise should give +Inf SNR, got %.2f", snr)
	}
}

func TestNormalizedSNR_ZeroSignal(t *testing.T) {
	signal := []int16{0, 0, 0}
	noise := []int16{100, 200, 300}

	snr := NormalizedSNR(signal, noise)

	if !math.IsInf(snr, -1) {
		t.Errorf("zero signal should give -Inf SNR, got %.2f", snr)
	}
}

func TestComputeNoiseVector(t *testing.T) {
	decoded := []int16{110, 220, 330}
	reference := []int16{100, 200, 300}

	noise := ComputeNoiseVector(decoded, reference)

	expected := []int16{10, 20, 30}
	for i, v := range noise {
		if v != expected[i] {
			t.Errorf("noise[%d]: expected %d, got %d", i, expected[i], v)
		}
	}
}

func TestComputeNoiseVector_Overflow(t *testing.T) {
	// Test clamping at int16 boundaries
	decoded := []int16{32767, -32768}
	reference := []int16{-32768, 32767}

	noise := ComputeNoiseVector(decoded, reference)

	// 32767 - (-32768) = 65535, clamped to 32767
	// -32768 - 32767 = -65535, clamped to -32768
	if noise[0] != 32767 {
		t.Errorf("expected clamped to 32767, got %d", noise[0])
	}
	if noise[1] != -32768 {
		t.Errorf("expected clamped to -32768, got %d", noise[1])
	}
}

func TestQualityFromSNR(t *testing.T) {
	// Test key SNR values
	testCases := []struct {
		snr float64
		q   float64
	}{
		{48.0, 0.0},    // Threshold
		{96.0, 100.0},  // Double the threshold SNR
		{24.0, -50.0},  // Half the threshold SNR
		{72.0, 50.0},   // 1.5x threshold
	}

	for _, tc := range testCases {
		q := QualityFromSNR(tc.snr)
		if math.Abs(q-tc.q) > 0.01 {
			t.Errorf("QualityFromSNR(%.1f): expected %.1f, got %.2f", tc.snr, tc.q, q)
		}
	}
}

func TestSNRFromQuality(t *testing.T) {
	// Test roundtrip
	testCases := []float64{0.0, 50.0, 100.0, -50.0}

	for _, q := range testCases {
		snr := SNRFromQuality(q)
		qBack := QualityFromSNR(snr)

		if math.Abs(qBack-q) > 0.01 {
			t.Errorf("roundtrip failed: Q=%.1f -> SNR=%.1f -> Q=%.2f", q, snr, qBack)
		}
	}
}

func TestComputeQuality_RealisticSignal(t *testing.T) {
	// Create a more realistic test with multi-frequency signal
	n := 48000 // 1 second at 48kHz
	reference := make([]int16, n)

	// Mix of frequencies
	for i := range reference {
		f1 := math.Sin(2 * math.Pi * 440 * float64(i) / 48000)  // A4
		f2 := math.Sin(2 * math.Pi * 880 * float64(i) / 48000)  // A5
		f3 := math.Sin(2 * math.Pi * 1760 * float64(i) / 48000) // A6
		reference[i] = int16(8000 * (f1 + 0.5*f2 + 0.25*f3))
	}

	// Decoded with small quantization noise
	decoded := make([]int16, n)
	for i := range decoded {
		// Add small random-ish noise (deterministic for test)
		noise := int16((i % 10) - 5)
		decoded[i] = reference[i] + noise
	}

	q := ComputeQuality(decoded, reference, 48000)

	t.Logf("Realistic signal with small noise: Q=%.2f", q)

	// Should easily pass with small noise
	if !QualityPasses(q) {
		t.Errorf("realistic signal with small noise should pass, got Q=%.2f", q)
	}
}
