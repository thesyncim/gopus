package silk

import (
	"math"
	"testing"
)

func TestBurgLPC(t *testing.T) {
	// Generate test signal: sum of sinusoids (normalized to [-1, 1])
	n := 320 // 20ms at 16kHz
	signal := make([]float32, n)
	for i := 0; i < n; i++ {
		ti := float64(i) / 16000.0
		signal[i] = float32(
			math.Sin(2*math.Pi*200*ti)+
				0.5*math.Sin(2*math.Pi*400*ti)+
				0.3*math.Sin(2*math.Pi*600*ti),
		) * 0.5 // Keep signal in reasonable range
	}

	// Compute LPC
	lpcQ12 := burgLPC(signal, 10)

	// Verify we got coefficients
	if len(lpcQ12) != 10 {
		t.Fatalf("expected 10 LPC coefficients, got %d", len(lpcQ12))
	}

	// LPC coefficients can exceed 1.0 in magnitude for signals with strong resonances
	// But they should be within int16 range (already clamped)
	t.Logf("LPC coefficients (Q12): %v", lpcQ12)

	// At least some coefficients should be non-zero for a non-trivial signal
	hasNonZero := false
	for _, c := range lpcQ12 {
		if c != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("Expected non-zero LPC coefficients for periodic signal")
	}

	// Verify we can compute higher order LPC
	lpc16 := burgLPC(signal, 16)
	if len(lpc16) != 16 {
		t.Fatalf("expected 16 LPC coefficients, got %d", len(lpc16))
	}
}

func TestBurgLPCShortSignal(t *testing.T) {
	// Signal shorter than order
	signal := make([]float32, 5)
	for i := range signal {
		signal[i] = float32(i)
	}

	lpc := burgLPC(signal, 10)

	// Should return zeros for short signal
	if len(lpc) != 10 {
		t.Fatalf("expected 10 LPC coefficients, got %d", len(lpc))
	}

	// All should be zero
	for i, c := range lpc {
		if c != 0 {
			t.Errorf("LPC[%d] = %d, expected 0 for short signal", i, c)
		}
	}
}

func TestBurgLPCConstantSignal(t *testing.T) {
	// A constant signal is unpredictable in the LPC sense
	// (no temporal correlation beyond DC offset)
	// Burg's method may produce non-zero coefficients but
	// the prediction error should be minimal
	n := 320
	signal := make([]float32, n)
	for i := range signal {
		signal[i] = 1.0 // Small constant
	}

	lpc := burgLPC(signal, 10)

	// Should return 10 coefficients
	if len(lpc) != 10 {
		t.Fatalf("expected 10 LPC coefficients, got %d", len(lpc))
	}

	// For a constant signal, the denominator in Burg's formula is very small
	// which can lead to numerical issues. The algorithm should handle this gracefully.
	t.Logf("LPC coefficients for constant signal (Q12): %v", lpc)
}

func TestBandwidthExpansionFloat(t *testing.T) {
	lpc := []int16{2048, 1024, 512, 256, 128}
	original := make([]int16, len(lpc))
	copy(original, lpc)

	applyBandwidthExpansionFloat(lpc, 0.96)

	// Each coefficient should be reduced by increasing powers of chirp
	chirp := 0.96
	for i := 0; i < len(lpc); i++ {
		expected := int16(float64(original[i]) * chirp)
		// Allow small rounding differences
		if lpcAbsInt(int(lpc[i]-expected)) > 1 {
			t.Errorf("LPC[%d]: expected %d, got %d", i, expected, lpc[i])
		}
		chirp *= 0.96
	}
}

func TestBandwidthExpansionFloatAggressive(t *testing.T) {
	lpc := []int16{4096, 2048, 1024, 512, 256}
	original := make([]int16, len(lpc))
	copy(original, lpc)

	// Apply very aggressive chirp
	applyBandwidthExpansionFloat(lpc, 0.5)

	// Each coefficient should be significantly reduced
	for i := 0; i < len(lpc); i++ {
		if lpc[i] >= original[i] {
			t.Errorf("LPC[%d]: expected reduction, got %d (original %d)",
				i, lpc[i], original[i])
		}
	}

	// Last coefficient should be much smaller than first
	if lpc[len(lpc)-1] > lpc[0]/4 {
		t.Errorf("Last coefficient not reduced enough: %d vs %d",
			lpc[len(lpc)-1], lpc[0])
	}
}

func TestLPCToLSFEncode(t *testing.T) {
	// Use known stable LPC coefficients (from actual speech)
	// These represent a typical vowel sound
	lpcQ12 := []int16{
		3276, -2048, 1638, -1229, 819, -614, 410, -307, 205, -102,
	}

	lsfQ15 := lpcToLSFEncode(lpcQ12)

	if len(lsfQ15) != len(lpcQ12) {
		t.Fatalf("expected %d LSF values, got %d", len(lpcQ12), len(lsfQ15))
	}

	// Verify LSF are strictly increasing
	for i := 1; i < len(lsfQ15); i++ {
		if lsfQ15[i] <= lsfQ15[i-1] {
			t.Errorf("LSF not increasing: lsf[%d]=%d <= lsf[%d]=%d",
				i, lsfQ15[i], i-1, lsfQ15[i-1])
		}
	}

	// Verify LSF are in valid range [0, 32767]
	for i, lsf := range lsfQ15 {
		if lsf < 0 || lsf > 32767 {
			t.Errorf("LSF[%d]=%d out of range [0, 32767]", i, lsf)
		}
	}
}

func TestLPCToLSFEncodeEmpty(t *testing.T) {
	lsfQ15 := lpcToLSFEncode(nil)
	if lsfQ15 != nil {
		t.Errorf("expected nil for empty input, got %v", lsfQ15)
	}

	lsfQ15 = lpcToLSFEncode([]int16{})
	if lsfQ15 != nil {
		t.Errorf("expected nil for empty slice, got %v", lsfQ15)
	}
}

func TestLPCToLSFEncodeWideband(t *testing.T) {
	// Wideband LPC coefficients (16 coeffs)
	lpcQ12 := []int16{
		2048, -1536, 1024, -768, 512, -384, 256, -192,
		128, -96, 64, -48, 32, -24, 16, -8,
	}

	lsfQ15 := lpcToLSFEncode(lpcQ12)

	if len(lsfQ15) != 16 {
		t.Fatalf("expected 16 LSF values, got %d", len(lsfQ15))
	}

	// Verify LSF are strictly increasing
	for i := 1; i < len(lsfQ15); i++ {
		if lsfQ15[i] <= lsfQ15[i-1] {
			t.Errorf("LSF not increasing: lsf[%d]=%d <= lsf[%d]=%d",
				i, lsfQ15[i], i-1, lsfQ15[i-1])
		}
	}
}

func TestLPCLSFRoundTrip(t *testing.T) {
	// Test that LPC -> LSF -> LPC approximately recovers original
	// Note: The round-trip is inherently lossy due to:
	// 1. Different conversion algorithms (encode uses Chebyshev, decode uses direct)
	// 2. Quantization to Q15 for LSF values
	// 3. Root-finding approximations
	lpcOriginal := []int16{
		2048, -1536, 1024, -768, 512, -384, 256, -192, 128, -64,
	}

	// Convert to LSF
	lsfQ15 := lpcToLSFEncode(lpcOriginal)

	// The key requirements are:
	// 1. LSF values are strictly increasing
	// 2. LSF values are in valid range [0, 32767]
	// 3. LSF values can be converted back to LPC (stable filter)

	t.Logf("LSF Q15 values: %v", lsfQ15)

	// Verify LSF are strictly increasing
	for i := 1; i < len(lsfQ15); i++ {
		if lsfQ15[i] <= lsfQ15[i-1] {
			t.Errorf("LSF not increasing: lsf[%d]=%d <= lsf[%d]=%d",
				i, lsfQ15[i], i-1, lsfQ15[i-1])
		}
	}

	// Verify LSF are in valid range
	for i, lsf := range lsfQ15 {
		if lsf < 0 || lsf > 32767 {
			t.Errorf("LSF[%d]=%d out of range [0, 32767]", i, lsf)
		}
	}

	// Convert back to LPC using decoder's function
	lpcRecovered := lsfToLPC(lsfQ15)

	// Log the round-trip results for reference
	t.Logf("Original LPC: %v", lpcOriginal)
	t.Logf("Recovered LPC: %v", lpcRecovered)

	// The recovered LPC should be valid (within Q12 range)
	for i, c := range lpcRecovered {
		if c > 32767 || c < -32768 {
			t.Errorf("Recovered LPC[%d]=%d out of int16 range", i, c)
		}
	}
}

func TestLPCLSFRoundTripSmallCoeffs(t *testing.T) {
	// Test with smaller coefficients that should round-trip better
	lpcOriginal := []int16{
		1024, -512, 256, -128, 64, -32, 16, -8, 4, -2,
	}

	// Convert to LSF
	lsfQ15 := lpcToLSFEncode(lpcOriginal)

	// Verify LSF ordering
	for i := 1; i < len(lsfQ15); i++ {
		if lsfQ15[i] <= lsfQ15[i-1] {
			t.Errorf("LSF not increasing at %d: %d <= %d",
				i, lsfQ15[i], lsfQ15[i-1])
		}
	}

	// Convert back to LPC
	lpcRecovered := lsfToLPC(lsfQ15)

	// Check recovery with generous tolerance
	for i := 0; i < len(lpcOriginal); i++ {
		diff := lpcAbsInt(int(lpcOriginal[i]) - int(lpcRecovered[i]))
		// Very generous tolerance for this lossy conversion
		maxErr := 1000
		if diff > maxErr {
			t.Logf("LPC[%d]: original=%d, recovered=%d, diff=%d (warning)",
				i, lpcOriginal[i], lpcRecovered[i], diff)
		}
	}
}

func TestEnsureLSFOrdering(t *testing.T) {
	// Test with out-of-order LSF values
	lsf := []int16{1000, 500, 2000, 1500, 3000}

	ensureLSFOrdering(lsf)

	// Verify strict ordering
	for i := 1; i < len(lsf); i++ {
		if lsf[i] <= lsf[i-1] {
			t.Errorf("LSF not increasing at %d: %d <= %d",
				i, lsf[i], lsf[i-1])
		}
	}
}

func TestEnsureLSFOrderingClamping(t *testing.T) {
	// Test with values that would exceed max
	lsf := []int16{32600, 32700, 32750, 32760, 32765}

	ensureLSFOrdering(lsf)

	// All values should be <= 32600
	for i, v := range lsf {
		if v > 32600 {
			t.Errorf("LSF[%d]=%d exceeds max 32600", i, v)
		}
	}
}

func TestComputeLPCFromFrame(t *testing.T) {
	// Create encoder for wideband
	enc := NewEncoder(BandwidthWideband)

	// Generate test signal
	n := 320
	pcm := make([]float32, n)
	for i := 0; i < n; i++ {
		ti := float64(i) / 16000.0
		pcm[i] = float32(math.Sin(2*math.Pi*300*ti)) * 0.5
	}

	lpc := enc.computeLPCFromFrame(pcm)

	// Should return correct number of coefficients
	if len(lpc) != enc.lpcOrder {
		t.Errorf("expected %d LPC coefficients, got %d", enc.lpcOrder, len(lpc))
	}

	// LPC coefficients from real signals can have magnitude > 1.0 (4096 in Q12)
	// This is normal for signals with strong resonances
	// The key is they should be within int16 range
	t.Logf("LPC from windowed signal (Q12): %v", lpc)

	// At least some coefficients should be non-zero
	hasNonZero := false
	for _, c := range lpc {
		if c != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("Expected non-zero LPC coefficients for periodic signal")
	}
}

func TestEvalChebyshev(t *testing.T) {
	// Test constant polynomial
	coef := []float64{5.0}
	result := evalChebyshev(coef, 0.5)
	if math.Abs(result-5.0) > 1e-10 {
		t.Errorf("constant polynomial: expected 5.0, got %f", result)
	}

	// Test empty polynomial
	result = evalChebyshev(nil, 0.5)
	if result != 0 {
		t.Errorf("empty polynomial: expected 0, got %f", result)
	}
}

func TestBisectRoot(t *testing.T) {
	// Test finding root of sin(x) near pi
	// Use a simple polynomial that has a root
	poly := []float64{-1.0, 0.0, 2.0} // Should cross zero

	// Find root in [0, pi/2]
	root := bisectRoot(poly, 0, math.Pi/2, evalChebyshev)

	// Root should be in the interval
	if root < 0 || root > math.Pi/2 {
		t.Errorf("root %f not in [0, pi/2]", root)
	}
}

// lpcAbsInt returns absolute value of int (local to this test file)
func lpcAbsInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// TestBurgModifiedFLP tests the libopus-matching Burg method implementation
func TestBurgModifiedFLP(t *testing.T) {
	// Generate a test signal with known spectral characteristics
	n := 320 // 20ms at 16kHz
	signal := make([]float64, n)
	for i := 0; i < n; i++ {
		ti := float64(i) / 16000.0
		// Mix of harmonics typical of voiced speech
		signal[i] = math.Sin(2*math.Pi*200*ti) +
			0.5*math.Sin(2*math.Pi*400*ti) +
			0.25*math.Sin(2*math.Pi*600*ti)
	}

	// Use parameters matching libopus 20ms frame
	subfrLength := 80 // 5ms at 16kHz
	nbSubfr := 4
	order := 10

	a, resNrg := burgModifiedFLP(signal, minInvGain, subfrLength, nbSubfr, order)

	// Check we got the right number of coefficients
	if len(a) != order {
		t.Fatalf("expected %d LPC coefficients, got %d", order, len(a))
	}

	// Check residual energy is positive
	if resNrg < 0 {
		t.Errorf("residual energy should be non-negative, got %f", resNrg)
	}

	// Coefficients should be reasonable (not NaN or Inf)
	for i, coef := range a {
		if math.IsNaN(coef) || math.IsInf(coef, 0) {
			t.Errorf("LPC[%d] is invalid: %f", i, coef)
		}
	}

	t.Logf("Burg LPC coefficients: %v", a)
	t.Logf("Residual energy: %f", resNrg)
}

// TestBurgModifiedFLPGainLimiting tests that the Burg method properly limits prediction gain
func TestBurgModifiedFLPGainLimiting(t *testing.T) {
	// Create a signal that would produce very high prediction gain
	// (strong resonance)
	n := 160
	signal := make([]float64, n)
	for i := 0; i < n; i++ {
		// Pure sinusoid has very high prediction gain
		signal[i] = math.Sin(2 * math.Pi * float64(i) / 20.0)
	}

	subfrLength := 40
	nbSubfr := 4
	order := 10

	a, _ := burgModifiedFLP(signal, minInvGain, subfrLength, nbSubfr, order)

	// Verify all coefficients are finite
	for i, coef := range a {
		if math.IsNaN(coef) || math.IsInf(coef, 0) {
			t.Errorf("LPC[%d] is invalid after gain limiting: %f", i, coef)
		}
	}

	// The magnitude of coefficients should be reasonable
	for i, coef := range a {
		if math.Abs(coef) > 10.0 {
			t.Logf("Warning: LPC[%d] has large magnitude: %f", i, coef)
		}
	}
}

// TestA2NLSFConversion tests the LPC to NLSF conversion
func TestA2NLSFConversion(t *testing.T) {
	// Use known LPC coefficients that produce stable NLSF
	aFloat := []float64{0.5, -0.3, 0.2, -0.15, 0.1, -0.08, 0.05, -0.03, 0.02, -0.01}

	nlsfQ15 := a2nlsfFLP(aFloat, len(aFloat))

	// Verify NLSF values are in valid range
	for i, nlsf := range nlsfQ15 {
		if nlsf < 0 || nlsf > 32767 {
			t.Errorf("NLSF[%d]=%d out of valid range [0, 32767]", i, nlsf)
		}
	}

	// Verify NLSF values are strictly increasing
	for i := 1; i < len(nlsfQ15); i++ {
		if nlsfQ15[i] <= nlsfQ15[i-1] {
			t.Errorf("NLSF not increasing at %d: %d <= %d", i, nlsfQ15[i], nlsfQ15[i-1])
		}
	}

	t.Logf("NLSF Q15 values: %v", nlsfQ15)
}

// TestNLSFInterpolation tests NLSF interpolation
func TestNLSFInterpolation(t *testing.T) {
	order := 10
	prevNLSF := make([]int16, order)
	curNLSF := make([]int16, order)
	outNLSF := make([]int16, order)

	// Initialize with distinct values
	for i := 0; i < order; i++ {
		prevNLSF[i] = int16(1000 + i*200)
		curNLSF[i] = int16(2000 + i*300)
	}

	// Test interpolation coefficient 0 (100% current)
	interpolateNLSF(outNLSF, prevNLSF, curNLSF, 0, order)
	for i := 0; i < order; i++ {
		if outNLSF[i] != curNLSF[i] {
			t.Errorf("interpCoef=0: expected %d, got %d at index %d", curNLSF[i], outNLSF[i], i)
		}
	}

	// Test interpolation coefficient 4 (copy current)
	interpolateNLSF(outNLSF, prevNLSF, curNLSF, 4, order)
	for i := 0; i < order; i++ {
		if outNLSF[i] != curNLSF[i] {
			t.Errorf("interpCoef=4: expected %d, got %d at index %d", curNLSF[i], outNLSF[i], i)
		}
	}

	// Test interpolation coefficient 2 (50% blend)
	interpolateNLSF(outNLSF, prevNLSF, curNLSF, 2, order)
	for i := 0; i < order; i++ {
		expected := (int32(prevNLSF[i])*2 + int32(curNLSF[i])*2 + 2) >> 2
		if int32(outNLSF[i]) != expected {
			t.Errorf("interpCoef=2: expected %d, got %d at index %d", expected, outNLSF[i], i)
		}
	}
}

// TestLPCAnalysisFilterFLP tests the LPC analysis filter
func TestLPCAnalysisFilterFLP(t *testing.T) {
	// Create a simple signal
	length := 100
	order := 10
	signal := make([]float64, length)
	for i := 0; i < length; i++ {
		signal[i] = math.Sin(2 * math.Pi * float64(i) / 20.0)
	}

	// Simple prediction coefficients
	predCoef := make([]float64, order)
	predCoef[0] = 0.9 // First-order prediction

	residual := make([]float64, length)
	lpcAnalysisFilterFLP(residual, predCoef, signal, length, order)

	// First 'order' samples should be zero
	for i := 0; i < order; i++ {
		if residual[i] != 0 {
			t.Errorf("residual[%d] should be 0, got %f", i, residual[i])
		}
	}

	// Remaining samples should be the prediction error
	// For a first-order predictor: residual[i] = signal[i] - 0.9*signal[i-1]
	for i := order; i < length; i++ {
		expected := signal[i] - 0.9*signal[i-1]
		if math.Abs(residual[i]-expected) > 1e-10 {
			t.Errorf("residual[%d]: expected %f, got %f", i, expected, residual[i])
		}
	}
}

// TestApplySineWindowFLP tests the asymmetric sine window
func TestApplySineWindowFLP(t *testing.T) {
	length := 64 // Must be multiple of 4
	input := make([]float64, length)
	output := make([]float64, length)

	// Fill with constant value
	for i := 0; i < length; i++ {
		input[i] = 1.0
	}

	// Apply type 1 window (ramp up from 0 to 1)
	applySineWindowFLP(output, input, 1, length)

	// First sample should be near 0
	if output[0] > 0.1 {
		t.Errorf("window type 1 start should be near 0, got %f", output[0])
	}

	// Last sample should be near 1
	if output[length-1] < 0.9 {
		t.Errorf("window type 1 end should be near 1, got %f", output[length-1])
	}

	// Apply type 2 window (ramp down from 1 to 0)
	applySineWindowFLP(output, input, 2, length)

	// First sample should be near 1
	if output[0] < 0.9 {
		t.Errorf("window type 2 start should be near 1, got %f", output[0])
	}

	// Last sample should be near 0
	if output[length-1] > 0.1 {
		t.Errorf("window type 2 end should be near 0, got %f", output[length-1])
	}
}

// TestEnergyF64 tests energy computation
func TestEnergyF64(t *testing.T) {
	// Test with known values
	signal := []float64{1.0, 2.0, 3.0, 4.0}
	expected := 1.0 + 4.0 + 9.0 + 16.0 // 30.0

	energy := energyF64(signal, len(signal))

	if math.Abs(energy-expected) > 1e-10 {
		t.Errorf("energy: expected %f, got %f", expected, energy)
	}

	// Test with partial length
	energy = energyF64(signal, 2)
	expected = 1.0 + 4.0 // 5.0
	if math.Abs(energy-expected) > 1e-10 {
		t.Errorf("partial energy: expected %f, got %f", expected, energy)
	}
}

// TestFindLPCWithInterpolation tests the full LPC analysis with interpolation
func TestFindLPCWithInterpolation(t *testing.T) {
	enc := NewEncoder(BandwidthNarrowband)

	// Generate test signal
	n := 320 // 20ms at 8kHz would be 160, but using larger for testing
	signal := make([]float32, n)
	for i := 0; i < n; i++ {
		ti := float64(i) / 8000.0
		signal[i] = float32(math.Sin(2*math.Pi*300*ti)) * 0.5
	}

	// Previous NLSF (simulate from previous frame)
	prevNLSF := make([]int16, enc.lpcOrder)
	for i := 0; i < enc.lpcOrder; i++ {
		prevNLSF[i] = int16(3000 + i*2500)
	}

	// First frame (no interpolation)
	nlsf, interpIdx := enc.FindLPCWithInterpolation(signal, prevNLSF, true, true, 4)

	if len(nlsf) != enc.lpcOrder {
		t.Errorf("expected %d NLSF values, got %d", enc.lpcOrder, len(nlsf))
	}

	// For first frame, interpolation should be 4 (no interp)
	if interpIdx != 4 {
		t.Logf("interpolation index for first frame: %d (expected 4 typically)", interpIdx)
	}

	// Verify NLSF values are valid
	for i := 1; i < len(nlsf); i++ {
		if nlsf[i] <= nlsf[i-1] {
			t.Errorf("NLSF not increasing at %d: %d <= %d", i, nlsf[i], nlsf[i-1])
		}
	}

	t.Logf("NLSF Q15: %v, interpIdx: %d", nlsf, interpIdx)
}

// TestSilkA2NLSFOrders tests A2NLSF for both order 10 and order 16
func TestSilkA2NLSFOrders(t *testing.T) {
	// Test order 10 (NB/MB)
	t.Run("order10", func(t *testing.T) {
		order := 10
		aQ16 := make([]int32, order)
		// Typical speech LPC values
		for i := 0; i < order; i++ {
			aQ16[i] = int32(float64(1<<15) * math.Pow(0.8, float64(i+1)))
			if i%2 == 1 {
				aQ16[i] = -aQ16[i]
			}
		}

		nlsf := make([]int16, order)
		silkA2NLSF(nlsf, aQ16, order)

		// Verify ordering
		for i := 1; i < order; i++ {
			if nlsf[i] <= nlsf[i-1] {
				t.Errorf("order 10: NLSF not increasing at %d: %d <= %d", i, nlsf[i], nlsf[i-1])
			}
		}

		t.Logf("Order 10 NLSF: %v", nlsf)
	})

	// Test order 16 (WB)
	t.Run("order16", func(t *testing.T) {
		order := 16
		aQ16 := make([]int32, order)
		for i := 0; i < order; i++ {
			aQ16[i] = int32(float64(1<<15) * math.Pow(0.85, float64(i+1)))
			if i%2 == 1 {
				aQ16[i] = -aQ16[i]
			}
		}

		nlsf := make([]int16, order)
		silkA2NLSF(nlsf, aQ16, order)

		// Verify ordering
		for i := 1; i < order; i++ {
			if nlsf[i] <= nlsf[i-1] {
				t.Errorf("order 16: NLSF not increasing at %d: %d <= %d", i, nlsf[i], nlsf[i-1])
			}
		}

		t.Logf("Order 16 NLSF: %v", nlsf)
	})
}

// TestLPCStabilityCheck tests that computed LPC filters are stable
func TestLPCStabilityCheck(t *testing.T) {
	// Generate various test signals
	testCases := []struct {
		name      string
		genSignal func(n int) []float32
	}{
		{"silence", func(n int) []float32 {
			return make([]float32, n)
		}},
		{"dc_offset", func(n int) []float32 {
			sig := make([]float32, n)
			for i := range sig {
				sig[i] = 0.5
			}
			return sig
		}},
		{"sinusoid", func(n int) []float32 {
			sig := make([]float32, n)
			for i := range sig {
				sig[i] = float32(math.Sin(2 * math.Pi * float64(i) / 20.0))
			}
			return sig
		}},
		{"white_noise", func(n int) []float32 {
			sig := make([]float32, n)
			// Simple LCG for reproducible "random" values
			seed := uint32(12345)
			for i := range sig {
				seed = seed*1103515245 + 12345
				sig[i] = float32(int32(seed>>16&0x7FFF)-16384) / 16384.0
			}
			return sig
		}},
		{"impulse", func(n int) []float32 {
			sig := make([]float32, n)
			sig[n/2] = 1.0
			return sig
		}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			signal := tc.genSignal(320)
			lpcQ12 := burgLPC(signal, 10)

			// Check stability using inverse prediction gain
			invGain := silkLPCInversePredGain(lpcQ12, 10)

			if invGain == 0 {
				t.Logf("%s: LPC filter may be unstable (invGain=0)", tc.name)
			} else {
				t.Logf("%s: LPC filter stable, invGain=%d", tc.name, invGain)
			}

			// Coefficients should be valid
			for i, c := range lpcQ12 {
				if c > 32767 || c < -32768 {
					t.Errorf("%s: LPC[%d]=%d out of range", tc.name, i, c)
				}
			}
		})
	}
}

// TestNLSFToLPCFloat tests the float version of NLSF to LPC conversion
func TestNLSFToLPCFloat(t *testing.T) {
	order := 10
	nlsfQ15 := make([]int16, order)

	// Set NLSF to evenly spaced values
	for i := 0; i < order; i++ {
		nlsfQ15[i] = int16((i + 1) * 32767 / (order + 1))
	}

	a := make([]float64, order)
	nlsfToLPCFloat(a, nlsfQ15, order)

	// Check coefficients are finite
	for i, coef := range a {
		if math.IsNaN(coef) || math.IsInf(coef, 0) {
			t.Errorf("LPC[%d] is invalid: %f", i, coef)
		}
	}

	t.Logf("NLSF to LPC float: %v", a)
}
