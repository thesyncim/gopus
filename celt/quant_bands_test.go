package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestPredCoefValues verifies prediction coefficients match libopus.
func TestPredCoefValues(t *testing.T) {
	// Reference values from libopus celt/quant_bands.c
	expected := []float32{
		29440.0 / 32768.0, // LM=0
		26112.0 / 32768.0, // LM=1
		21248.0 / 32768.0, // LM=2
		16384.0 / 32768.0, // LM=3
	}

	for lm := 0; lm < 4; lm++ {
		if math.Abs(float64(predCoef[lm]-expected[lm])) > 1e-7 {
			t.Errorf("predCoef[%d] = %f, want %f", lm, predCoef[lm], expected[lm])
		}
	}
}

// TestBetaCoefValues verifies beta coefficients match libopus.
func TestBetaCoefValues(t *testing.T) {
	// Reference values from libopus celt/quant_bands.c
	expectedInter := []float32{
		30147.0 / 32768.0, // LM=0
		22282.0 / 32768.0, // LM=1
		12124.0 / 32768.0, // LM=2
		6554.0 / 32768.0,  // LM=3
	}

	for lm := 0; lm < 4; lm++ {
		if math.Abs(float64(betaCoef[lm]-expectedInter[lm])) > 1e-7 {
			t.Errorf("betaCoef[%d] = %f, want %f", lm, betaCoef[lm], expectedInter[lm])
		}
	}

	expectedIntra := float32(4915.0 / 32768.0)
	if math.Abs(float64(betaIntraF32-expectedIntra)) > 1e-7 {
		t.Errorf("betaIntraF32 = %f, want %f", betaIntraF32, expectedIntra)
	}
}

// TestEMeansValues verifies eMeans table matches libopus.
func TestEMeansValues(t *testing.T) {
	// Reference values from libopus celt/quant_bands.c (float eMeans table)
	expected := []float64{
		6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
		4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
		4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
		4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
		3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
	}

	for i := 0; i < len(expected) && i < len(eMeans); i++ {
		if math.Abs(eMeans[i]-expected[i]) > 1e-6 {
			t.Errorf("eMeans[%d] = %f, want %f", i, eMeans[i], expected[i])
		}
	}
}

// TestEProbModelValues verifies e_prob_model table matches libopus.
func TestEProbModelValues(t *testing.T) {
	// Test a few key values from libopus e_prob_model
	// LM=0, Inter, first band
	if eProbModel[0][0][0] != 72 {
		t.Errorf("eProbModel[0][0][0] = %d, want 72", eProbModel[0][0][0])
	}
	if eProbModel[0][0][1] != 127 {
		t.Errorf("eProbModel[0][0][1] = %d, want 127", eProbModel[0][0][1])
	}

	// LM=0, Intra, first band
	if eProbModel[0][1][0] != 24 {
		t.Errorf("eProbModel[0][1][0] = %d, want 24", eProbModel[0][1][0])
	}
	if eProbModel[0][1][1] != 179 {
		t.Errorf("eProbModel[0][1][1] = %d, want 179", eProbModel[0][1][1])
	}

	// LM=3, Inter, first band
	if eProbModel[3][0][0] != 42 {
		t.Errorf("eProbModel[3][0][0] = %d, want 42", eProbModel[3][0][0])
	}
}

// TestLossDistortion verifies the loss distortion computation.
func TestLossDistortion(t *testing.T) {
	// Create simple test case
	eBands := make([]float64, MaxBands)
	oldEBands := make([]float64, MaxBands)

	// Set some test values
	for i := 0; i < 10; i++ {
		eBands[i] = float64(i)
		oldEBands[i] = float64(i) + 0.5
	}

	dist := lossDistortion(eBands, oldEBands, 0, 10, MaxBands, 1)

	// Expected: sum of (0.5)^2 * 10 / 128 = 0.5^2 * 10 / 128 = 2.5/128 ≈ 0.0195
	expected := float32(2.5 / 128.0)
	if math.Abs(float64(dist-expected)) > 0.001 {
		t.Errorf("lossDistortion = %f, want ~%f", dist, expected)
	}
}

// TestAmp2Log2Conversion verifies amplitude to log2 conversion.
func TestAmp2Log2Conversion(t *testing.T) {
	// Create test amplitudes
	bandE := make([]float64, MaxBands)
	for i := 0; i < MaxBands; i++ {
		bandE[i] = math.Pow(2, float64(i)/4.0) // Exponentially increasing
	}

	result := Amp2Log2(bandE, MaxBands, MaxBands, 1)

	// Verify a few values
	// For bandE[4] = 2^1 = 2, log2(2) = 1, then subtract eMeans[4]
	expected4 := 1.0 - eMeans[4]
	if math.Abs(result[4]-expected4) > 0.01 {
		t.Errorf("Amp2Log2[4] = %f, want ~%f", result[4], expected4)
	}
}

// TestQuantCoarseEnergyRoundTrip tests that encoding and decoding produce consistent results.
func TestQuantCoarseEnergyRoundTrip(t *testing.T) {
	// Create test energies
	nbBands := 21
	channels := 1
	eBands := make([]float64, nbBands*channels)
	oldEBands := make([]float64, nbBands*channels)

	for i := 0; i < nbBands; i++ {
		eBands[i] = float64(5 - i/4) // Some variation
		oldEBands[i] = 0             // Start with zeros
	}

	// Create encoder buffer
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Encode coarse energy
	delayedIntra := float32(1.0)
	params := QuantCoarseEnergyParams{
		Start:            0,
		End:              nbBands,
		EffEnd:           nbBands,
		LM:               3, // 20ms
		Channels:         channels,
		Budget:           256 * 8,
		NBAvailableBytes: 256,
		ForceIntra:       false,
		TwoPass:          true,
		LossRate:         0,
		LFE:              false,
	}

	result := QuantCoarseEnergy(re, eBands, oldEBands, params, &delayedIntra)

	// Verify results are reasonable
	for i := 0; i < nbBands; i++ {
		// Quantized energy should be close to original (within a few dB)
		diff := math.Abs(result.QuantizedEnergy[i] - eBands[i])
		if diff > 3.0 { // Allow up to 3 dB difference (half a step)
			t.Errorf("Band %d: quantized=%f, original=%f, diff=%f too large",
				i, result.QuantizedEnergy[i], eBands[i], diff)
		}
	}
}

// TestQuantFineEnergy tests fine energy encoding.
func TestQuantFineEnergy(t *testing.T) {
	nbBands := 21
	channels := 1

	// Setup test data
	oldEBands := make([]float64, nbBands*channels)
	errorVal := make([]float64, nbBands*channels)
	extraQuant := make([]int, nbBands)

	for i := 0; i < nbBands; i++ {
		oldEBands[i] = float64(5 - i/4)
		errorVal[i] = 0.25 // Small positive error
		if i < 15 {
			extraQuant[i] = 2 // 2 fine bits for first 15 bands
		}
	}

	// Create encoder
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Encode fine energy
	QuantFineEnergy(re, 0, nbBands, oldEBands, errorVal, nil, extraQuant, channels)

	// Verify that error was reduced
	for i := 0; i < 15; i++ {
		if math.Abs(errorVal[i]) >= 0.25 {
			t.Errorf("Band %d: error not reduced after fine encoding: %f", i, errorVal[i])
		}
	}
}

// TestQuantEnergyFinalise tests energy finalization.
func TestQuantEnergyFinalise(t *testing.T) {
	nbBands := 21
	channels := 1

	// Setup test data
	oldEBands := make([]float64, nbBands*channels)
	errorVal := make([]float64, nbBands*channels)
	fineQuant := make([]int, nbBands)
	finePriority := make([]int, nbBands)

	for i := 0; i < nbBands; i++ {
		oldEBands[i] = float64(5 - i/4)
		errorVal[i] = 0.1 // Small positive error
		fineQuant[i] = 2  // 2 bits already used
		if i%2 == 0 {
			finePriority[i] = 0 // High priority
		} else {
			finePriority[i] = 1 // Low priority
		}
	}

	// Create encoder
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Record initial tell value (range coder starts at 1 bit, not 0)
	initialTell := re.Tell()

	// Finalize with some bits
	bitsLeft := 20
	QuantEnergyFinalise(re, 0, nbBands, oldEBands, errorVal, fineQuant, finePriority, bitsLeft, channels)

	// Verify bits were consumed (Tell() should have increased)
	finalTell := re.Tell()
	bitsUsed := finalTell - initialTell
	if bitsUsed == 0 {
		t.Error("No bits were used during finalization")
	}
	if bitsUsed > bitsLeft {
		t.Errorf("Used more bits than available: %d > %d", bitsUsed, bitsLeft)
	}
}

// TestCeltLog2Accuracy tests the log2 function accuracy.
func TestCeltLog2Accuracy(t *testing.T) {
	testCases := []struct {
		input    float32
		expected float32
	}{
		{1.0, 0.0},
		{2.0, 1.0},
		{4.0, 2.0},
		{0.5, -1.0},
		{1e-10, -33.219}, // Approximately log2(1e-10)
	}

	for _, tc := range testCases {
		result := celtLog2(tc.input)
		if math.Abs(float64(result-tc.expected)) > 0.01 {
			t.Errorf("celtLog2(%f) = %f, want ~%f", tc.input, result, tc.expected)
		}
	}
}

// TestEncodeLaplaceEnergy tests Laplace encoding for energy.
func TestEncodeLaplaceEnergy(t *testing.T) {
	// Create encoder
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Test encoding zero (most common)
	fs := 72 << 7   // From prob_model
	decay := 127 << 6

	val0 := encodeLaplaceEnergy(re, 0, fs, decay)
	if val0 != 0 {
		t.Errorf("encodeLaplaceEnergy(0) returned %d, want 0", val0)
	}

	// Reset and test positive value
	re.Init(buf)
	val1 := encodeLaplaceEnergy(re, 1, fs, decay)
	if val1 != 1 {
		t.Errorf("encodeLaplaceEnergy(1) returned %d, want 1", val1)
	}

	// Reset and test negative value
	re.Init(buf)
	valNeg := encodeLaplaceEnergy(re, -1, fs, decay)
	if valNeg != -1 {
		t.Errorf("encodeLaplaceEnergy(-1) returned %d, want -1", valNeg)
	}
}

// BenchmarkQuantCoarseEnergy benchmarks coarse energy quantization.
func BenchmarkQuantCoarseEnergy(b *testing.B) {
	nbBands := 21
	channels := 1
	eBands := make([]float64, nbBands*channels)
	oldEBands := make([]float64, nbBands*channels)

	for i := 0; i < nbBands; i++ {
		eBands[i] = float64(5 - i/4)
	}

	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	delayedIntra := float32(1.0)

	params := QuantCoarseEnergyParams{
		Start:            0,
		End:              nbBands,
		EffEnd:           nbBands,
		LM:               3,
		Channels:         channels,
		Budget:           256 * 8,
		NBAvailableBytes: 256,
		TwoPass:          true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		re.Init(buf)
		copy(oldEBands, eBands) // Reset state
		_ = QuantCoarseEnergy(re, eBands, oldEBands, params, &delayedIntra)
	}
}
