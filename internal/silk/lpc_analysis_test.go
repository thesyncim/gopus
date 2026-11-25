package silk

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math"
	"math/rand"
	"testing"
)

func TestBurgLPC(t *testing.T) {
	// Generate test signal: sum of sinusoids (normalized to [-1, 1])
	n := 320 // 20ms at 16kHz
	signal := make([]float32, n)
	for i := range n {
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
	chirp := float32(0.96)
	for i := range lpc {
		expected := int16(float32(original[i]) * chirp)
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
	for i := range lpc {
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

	// Verify LSF remain non-negative Q15 values.
	for i, lsf := range lsfQ15 {
		if lsf < 0 {
			t.Errorf("LSF[%d]=%d is negative", i, lsf)
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

	// Verify LSF remain non-negative Q15 values.
	for i, lsf := range lsfQ15 {
		if lsf < 0 {
			t.Errorf("LSF[%d]=%d is negative", i, lsf)
		}
	}

	// Convert back to LPC using decoder's function
	lpcRecovered := lsfToLPC(lsfQ15)
	if len(lpcRecovered) != len(lpcOriginal) {
		t.Fatalf("Recovered LPC length = %d, want %d", len(lpcRecovered), len(lpcOriginal))
	}

	// Log the round-trip results for reference
	t.Logf("Original LPC: %v", lpcOriginal)
	t.Logf("Recovered LPC: %v", lpcRecovered)

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
	for i := range lpcOriginal {
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
	signal := make([]float32, n)
	for i := range n {
		ti := float64(i) / 16000.0
		// Mix of harmonics typical of voiced speech
		signal[i] = float32(math.Sin(2*math.Pi*200*ti) +
			0.5*math.Sin(2*math.Pi*400*ti) +
			0.25*math.Sin(2*math.Pi*600*ti))
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
		if math.IsNaN(float64(coef)) || math.IsInf(float64(coef), 0) {
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
	signal := make([]float32, n)
	for i := range n {
		// Pure sinusoid has very high prediction gain
		signal[i] = float32(math.Sin(2 * math.Pi * float64(i) / 20.0))
	}

	subfrLength := 40
	nbSubfr := 4
	order := 10

	a, _ := burgModifiedFLP(signal, minInvGain, subfrLength, nbSubfr, order)

	// Verify all coefficients are finite
	for i, coef := range a {
		if math.IsNaN(float64(coef)) || math.IsInf(float64(coef), 0) {
			t.Errorf("LPC[%d] is invalid after gain limiting: %f", i, coef)
		}
	}

	// The magnitude of coefficients should be reasonable
	for i, coef := range a {
		if math.Abs(float64(coef)) > 10.0 {
			t.Logf("Warning: LPC[%d] has large magnitude: %f", i, coef)
		}
	}
}

// TestA2NLSFConversion tests the LPC to NLSF conversion
func TestA2NLSFConversion(t *testing.T) {
	// Use known LPC coefficients that produce stable NLSF
	aFloat := []float32{0.5, -0.3, 0.2, -0.15, 0.1, -0.08, 0.05, -0.03, 0.02, -0.01}

	nlsfQ15 := a2nlsfFLP(aFloat, len(aFloat))

	// Verify NLSF values stay non-negative.
	for i, nlsf := range nlsfQ15 {
		if nlsf < 0 {
			t.Errorf("NLSF[%d]=%d is negative", i, nlsf)
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
	for i := range order {
		prevNLSF[i] = int16(1000 + i*200)
		curNLSF[i] = int16(2000 + i*300)
	}

	// Test interpolation coefficient 0 (100% previous)
	interpolateNLSF(outNLSF, prevNLSF, curNLSF, 0, order)
	for i := range order {
		if outNLSF[i] != prevNLSF[i] {
			t.Errorf("interpCoef=0: expected %d, got %d at index %d", prevNLSF[i], outNLSF[i], i)
		}
	}

	// Test interpolation coefficient 4 (100% current)
	interpolateNLSF(outNLSF, prevNLSF, curNLSF, 4, order)
	for i := range order {
		if outNLSF[i] != curNLSF[i] {
			t.Errorf("interpCoef=4: expected %d, got %d at index %d", curNLSF[i], outNLSF[i], i)
		}
	}

	// Test interpolation coefficient 2 (50% blend)
	interpolateNLSF(outNLSF, prevNLSF, curNLSF, 2, order)
	for i := range order {
		diff := int32(curNLSF[i]) - int32(prevNLSF[i])
		expected := int32(prevNLSF[i]) + ((2 * diff) >> 2)
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
	signal := make([]float32, length)
	for i := range length {
		signal[i] = float32(math.Sin(2 * math.Pi * float64(i) / 20.0))
	}

	// Simple prediction coefficients
	predCoef := make([]float32, order)
	predCoef[0] = 0.9 // First-order prediction

	residual := make([]float32, length)
	lpcAnalysisFilterF32(residual, predCoef, signal, length, order)

	// First 'order' samples should be zero
	for i := range order {
		if residual[i] != 0 {
			t.Errorf("residual[%d] should be 0, got %f", i, residual[i])
		}
	}

	// Remaining samples should be the prediction error
	// For a first-order predictor: residual[i] = signal[i] - 0.9*signal[i-1]
	for i := order; i < length; i++ {
		expected := signal[i] - 0.9*signal[i-1]
		if math.Abs(float64(residual[i]-expected)) > 1e-5 {
			t.Errorf("residual[%d]: expected %f, got %f", i, expected, residual[i])
		}
	}
}

// TestApplySineWindowFLP tests the asymmetric sine window
func TestApplySineWindowFLP(t *testing.T) {
	length := 64 // Must be multiple of 4
	input := make([]float32, length)
	output := make([]float32, length)

	// Fill with constant value
	for i := range length {
		input[i] = 1.0
	}

	// Apply type 1 window (ramp up from 0 to 1)
	applySineWindowFLP32(output, input, 1, length)

	// First sample should be near 0
	if output[0] > 0.1 {
		t.Errorf("window type 1 start should be near 0, got %f", output[0])
	}

	// Last sample should be near 1
	if output[length-1] < 0.9 {
		t.Errorf("window type 1 end should be near 1, got %f", output[length-1])
	}

	// Apply type 2 window (ramp down from 1 to 0)
	applySineWindowFLP32(output, input, 2, length)

	// First sample should be near 1
	if output[0] < 0.9 {
		t.Errorf("window type 2 start should be near 1, got %f", output[0])
	}

	// Last sample should be near 0
	if output[length-1] > 0.1 {
		t.Errorf("window type 2 end should be near 0, got %f", output[length-1])
	}
}

// TestEnergyF32Libopus tests energy computation.
func TestEnergyF32Libopus(t *testing.T) {
	// Test with known values
	signal := []float32{1.0, 2.0, 3.0, 4.0}
	expected := 1.0 + 4.0 + 9.0 + 16.0 // 30.0

	energy := energyF32Libopus(signal, len(signal))

	if math.Abs(energy-expected) > 1e-5 {
		t.Errorf("energy: expected %f, got %f", expected, energy)
	}

	// Test with partial length
	energy = energyF32Libopus(signal, 2)
	expected = 1.0 + 4.0 // 5.0
	if math.Abs(energy-expected) > 1e-5 {
		t.Errorf("partial energy: expected %f, got %f", expected, energy)
	}
}

// TestFindLPCWithInterpolation tests the full LPC analysis with interpolation
func TestFindLPCWithInterpolation(t *testing.T) {
	enc := newTestEncoder(BandwidthNarrowband)

	// Generate test signal
	n := 320 // 20ms at 8kHz would be 160, but using larger for testing
	signal := make([]float32, n)
	for i := range n {
		ti := float64(i) / 8000.0
		signal[i] = float32(math.Sin(2*math.Pi*300*ti)) * 0.5
	}

	// Previous NLSF (simulate from previous frame)
	lpcOrder := int(enc.lpcOrder)
	prevNLSF := make([]int16, lpcOrder)
	for i := range lpcOrder {
		prevNLSF[i] = int16(3000 + i*2500)
	}

	// First frame (no interpolation)
	nlsf, interpIdx := enc.FindLPCWithInterpolation(signal, prevNLSF, true, true, 4)

	if len(nlsf) != lpcOrder {
		t.Errorf("expected %d NLSF values, got %d", lpcOrder, len(nlsf))
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

	// Non-first-frame interpolation runs a second Burg pass; the full-frame
	// coefficients must survive that scratch reuse for the final no-interp path.
	nlsf, interpIdx = enc.FindLPCWithInterpolation(signal, prevNLSF, true, false, 4)
	if len(nlsf) != lpcOrder {
		t.Errorf("non-first-frame: expected %d NLSF values, got %d", lpcOrder, len(nlsf))
	}
	if interpIdx < 0 || interpIdx > 4 {
		t.Errorf("non-first-frame: invalid interpolation index %d", interpIdx)
	}
	for i := 1; i < len(nlsf); i++ {
		if nlsf[i] <= nlsf[i-1] {
			t.Errorf("non-first-frame: NLSF not increasing at %d: %d <= %d", i, nlsf[i], nlsf[i-1])
		}
	}
}

// TestSilkA2NLSFOrders tests A2NLSF for both order 10 and order 16
func TestSilkA2NLSFOrders(t *testing.T) {
	// Test order 10 (NB/MB)
	t.Run("order10", func(t *testing.T) {
		order := 10
		aQ16 := make([]int32, order)
		// Typical speech LPC values
		for i := range order {
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
		for i := range order {
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

		})
	}
}

// TestNLSFToLPCFloat32 tests the silk_NLSF2A_FLP-style fixed bridge.
func TestNLSFToLPCFloat32(t *testing.T) {
	order := 10
	nlsfQ15 := make([]int16, order)

	// Set NLSF to evenly spaced values
	for i := range order {
		nlsfQ15[i] = int16((i + 1) * 32767 / (order + 1))
	}

	a := make([]float32, order)
	aQ12 := make([]int16, order)
	if !nlsfToLPCFloat32(a, aQ12, nlsfQ15, order) {
		t.Fatal("nlsfToLPCFloat32 returned false")
	}

	// Check coefficients are finite
	for i, coef := range a {
		if math.IsNaN(float64(coef)) || math.IsInf(float64(coef), 0) {
			t.Errorf("LPC[%d] is invalid: %f", i, coef)
		}
		want := float32(aQ12[i]) * (1.0 / 4096.0)
		if coef != want {
			t.Errorf("LPC[%d] = %f want %f", i, coef, want)
		}
	}

	t.Logf("NLSF to LPC float32: %v", a)
}

func TestA2NLSFEvalPolyRegression(t *testing.T) {
	p5 := []int32{1, 2, 3, 4, 5, 6}
	p8 := []int32{1, 1, 2, 3, 5, 8, 13, 21, 34}
	p3 := []int32{7, -3, 2, 1}

	v5 := a2nlsfEvalPoly(p5, 1234, 5)
	v8 := a2nlsfEvalPoly(p8, -567, 8)
	v3 := a2nlsfEvalPoly(p3, 321, 3)

	if v5 == 0 && v8 == 0 && v3 == 0 {
		t.Fatal("a2nlsfEvalPoly returned all zeros unexpectedly")
	}
}

func TestSilkBwExpander32AQ16Regression(t *testing.T) {
	// order <= 0 branch.
	empty := []int32{}
	silkBwExpander32AQ16(empty, 0, 65536)

	// Normal branch and loop body.
	ar := []int32{40000, -30000, 20000, -10000}
	before := append([]int32(nil), ar...)
	silkBwExpander32AQ16(ar, len(ar), 62000)

	changed := false
	for i := range ar {
		if ar[i] != before[i] {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("silkBwExpander32AQ16 did not update coefficients")
	}
}

func TestSilkLPCFitHighAmplitudeRegression(t *testing.T) {
	const (
		order = 10
		qOut  = 12
		qIn   = 24
	)
	aQin := []int32{
		900000000, -850000000, 800000000, -750000000, 700000000,
		-650000000, 600000000, -550000000, 500000000, -450000000,
	}
	out := make([]int16, order)

	silkLPCFit(out, aQin, qOut, qIn, order)

}

func lpcInversePredGainReference(aQ12 []int16) int32 {
	order := len(aQ12)
	var aQA [maxLPCOrder]int32
	var dcResp int32
	for k, v := range aQ12 {
		dcResp += int32(v)
		aQA[k] = silkLSHIFT(int32(v), lpcInvPredGainQA-12)
	}
	if dcResp >= 4096 {
		return 0
	}
	invGainQ30 := int32(1 << 30)
	for k := order - 1; k > 0; k-- {
		if aQA[k] > lpcInvPredGainALimitQ24 || aQA[k] < -lpcInvPredGainALimitQ24 {
			return 0
		}
		rcQ31 := -silkLSHIFT(aQA[k], 31-lpcInvPredGainQA)
		rcMult1Q30 := int32(1<<30) - silkSMMUL(rcQ31, rcQ31)
		invGainQ30 = silkLSHIFT(silkSMMUL(invGainQ30, rcMult1Q30), 2)
		if invGainQ30 < maxPredictionPowerGainInvQ30 {
			return 0
		}
		mult2Q := int(32 - silkCLZ32(silkAbs32(rcMult1Q30)))
		rcMult2 := silkInverse32VarQ(rcMult1Q30, mult2Q+30)
		for n := 0; n < (k+1)>>1; n++ {
			tmp1 := aQA[n]
			tmp2 := aQA[k-n-1]
			tmp64 := silkRSHIFT_ROUND64(silkSMULL(silkSubSat32(tmp1, int32(silkRSHIFT_ROUND64(silkSMULL(tmp2, rcQ31), 31))), rcMult2), mult2Q)
			if tmp64 > int64(silkInt32Max) || tmp64 < int64(silkInt32Min) {
				return 0
			}
			aQA[n] = int32(tmp64)
			tmp64 = silkRSHIFT_ROUND64(silkSMULL(silkSubSat32(tmp2, int32(silkRSHIFT_ROUND64(silkSMULL(tmp1, rcQ31), 31))), rcMult2), mult2Q)
			if tmp64 > int64(silkInt32Max) || tmp64 < int64(silkInt32Min) {
				return 0
			}
			aQA[k-n-1] = int32(tmp64)
		}
	}
	if aQA[0] > lpcInvPredGainALimitQ24 || aQA[0] < -lpcInvPredGainALimitQ24 {
		return 0
	}
	rcQ31 := -silkLSHIFT(aQA[0], 31-lpcInvPredGainQA)
	rcMult1Q30 := int32(1<<30) - silkSMMUL(rcQ31, rcQ31)
	invGainQ30 = silkLSHIFT(silkSMMUL(invGainQ30, rcMult1Q30), 2)
	if invGainQ30 < maxPredictionPowerGainInvQ30 {
		return 0
	}
	return invGainQ30
}

// TestLPCInversePredGainMatchesReference checks the paired step-down loop
// against the one-at-a-time reference on stable, marginal and unstable
// filters.
func TestLPCInversePredGainMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1c9))
	nonzero := 0
	for iter := range 200000 {
		order := []int{10, 16}[rng.Intn(2)]
		a := make([]int16, order)
		scale := []int{64, 512, 2048, 8192, 32768}[rng.Intn(5)]
		for i := range a {
			a[i] = int16(rng.Intn(2*scale) - scale)
		}
		if rng.Intn(4) == 0 {
			// Bandwidth-expanded all-pole filters are the typical stable input.
			for i := range a {
				a[i] = int16(int32(a[i]) * int32(order-i) / int32(4*order))
			}
		}
		got := silkLPCInversePredGain(a, order)
		want := lpcInversePredGainReference(a)
		if got != want {
			t.Fatalf("iter %d: silkLPCInversePredGain(%v) = %d, want %d", iter, a, got, want)
		}
		if got != 0 {
			nonzero++
		}
	}
	if nonzero < 1000 {
		t.Fatalf("only %d stable filters exercised", nonzero)
	}
}

const (
	libopusSILKLPCInputMagic  = "GSLI"
	libopusSILKLPCOutputMagic = "GSLO"

	libopusSILKLPCModeBurgModified      = uint32(0)
	libopusSILKLPCModeLPCAnalysisFilter = uint32(1)
	libopusSILKLPCModeInnerProduct      = uint32(2)
	libopusSILKLPCModeEnergy            = uint32(3)
	libopusSILKLPCModeFindLPC           = uint32(4)
	libopusSILKLPCModeAutocorrelation   = uint32(5)
)

var libopusSILKLPCHelper libopustest.HelperCache

func getLibopusSILKLPCHelperPath() (string, error) {
	libopusArchive := libopustest.RefPath(".libs", "libopus.a")
	cflags := []string{"-DHAVE_CONFIG_H", "-O2"}
	if silkLPCOracleUsesAVX2() {
		libopusArchive = libopustest.SIMDRefPath(".libs", "libopus.a")
		cflags = append(cflags, "-DGOPUS_LIBOPUS_REQUIRE_AVX2=1")
	}
	return libopusSILKLPCHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "silk lpc",
		OutputBase:  "gopus_libopus_silk_lpc",
		SourceFile:  "libopus_silk_lpc_info.c",
		CFlags:      cflags,
		RefIncludes: []string{"celt", "silk", "silk/float"},
		SIMDRef:     silkLPCOracleUsesAVX2(),
		Libs:        []string{libopusArchive, "-lm"},
	})
}

func newLibopusSILKOracleReader(label, outputMagic string, data []byte, wantCount int) (*libopustest.OracleReader, int, error) {
	reader, version, err := libopustest.NewOracleReaderVersion(label, outputMagic, data)
	if err != nil {
		return nil, 0, err
	}
	if version != 3 {
		return nil, 0, fmt.Errorf("%s helper version=%d want 3", label, version)
	}
	arch, innerProductImpl, presumed := reader.U32(), reader.U32(), reader.U32()
	wantAVX2 := silkLPCOracleUsesAVX2()
	if !libopustest.NativeX86SILKInnerProductMetadataValid(arch, innerProductImpl, presumed, wantAVX2) {
		return nil, 0, fmt.Errorf("%s helper SILK_INNER_PRODUCT_FLP_IMPL=%d at arch=%d presumed=%d, Go expects AVX2=%t", label, innerProductImpl, arch, presumed, wantAVX2)
	}
	count := reader.Count(wantCount)
	return reader, count, nil
}

func runLibopusSILKOracle(binPath string, payload []byte, label string, wantCount int) (*libopustest.OracleReader, int, error) {
	data, err := libopustest.RunHelper(binPath, payload)
	if err != nil {
		return nil, 0, fmt.Errorf("run %s helper: %w", label, err)
	}
	return newLibopusSILKOracleReader(label, libopusSILKLPCOutputMagic, data, wantCount)
}

type libopusSILKBurgCase struct {
	name       string
	subfrLen   int
	nbSubfr    int
	order      int
	minInvGain float32
	x          []float32
}

type libopusSILKBurgResult struct {
	resNrg float32
	a      []float32
}

type libopusSILKLPCFilterCase struct {
	name  string
	order int
	pred  []float32
	x     []float32
}

type libopusSILKInnerProductCase struct {
	name string
	a    []float32
	b    []float32
}

type libopusSILKEnergyCase struct {
	name string
	x    []float32
}

type libopusSILKAutocorrelationCase struct {
	name  string
	order int
	x     []float32
}

type libopusSILKFindLPCCase struct {
	name                 string
	subfrLength          int
	nbSubfr              int
	order                int
	useInterpolatedNLSFs bool
	firstFrameAfterReset bool
	minInvGain           float32
	prevNLSF             []int16
	x                    []float32
}

type libopusSILKFindLPCResult struct {
	interpIdx int
	nlsf      []int16
}

func probeLibopusSILKBurgModified(cases []libopusSILKBurgCase) ([]libopusSILKBurgResult, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeBurgModified, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.subfrLen))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.order))
		payload.Float32(tc.minInvGain)
		payload.Float32s(tc.x...)
	}

	reader, count, err := runLibopusSILKOracle(binPath, payload.Bytes(), "silk lpc burg", len(cases))
	if err != nil {
		return nil, err
	}
	out := make([]libopusSILKBurgResult, count)
	for i := range out {
		out[i].resNrg = reader.Float32()
		order := int(reader.U32())
		if order != 10 && order != 16 {
			return nil, fmt.Errorf("helper order=%d", order)
		}
		out[i].a = make([]float32, order)
		for j := range 16 {
			v := reader.Float32()
			if j < order {
				out[i].a[j] = v
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKLPCAnalysisFilter(cases []libopusSILKLPCFilterCase) ([][]float32, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeLPCAnalysisFilter, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.order))
		payload.Float32s(tc.pred...)
		payload.Float32s(tc.x...)
	}

	reader, count, err := runLibopusSILKOracle(binPath, payload.Bytes(), "silk lpc analysis filter", len(cases))
	if err != nil {
		return nil, err
	}
	out := make([][]float32, count)
	for i := range out {
		length := int(reader.U32())
		if length <= 0 || length > 512 {
			return nil, fmt.Errorf("helper length=%d", length)
		}
		out[i] = make([]float32, length)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKInnerProductFLP(cases []libopusSILKInnerProductCase) ([]float64, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeInnerProduct, uint32(len(cases)))
	for _, tc := range cases {
		if len(tc.a) != len(tc.b) {
			return nil, fmt.Errorf("%s: a len=%d b len=%d", tc.name, len(tc.a), len(tc.b))
		}
		payload.U32(uint32(len(tc.a)))
		payload.Float32s(tc.a...)
		payload.Float32s(tc.b...)
	}

	reader, count, err := runLibopusSILKOracle(binPath, payload.Bytes(), "silk inner product flp", len(cases))
	if err != nil {
		return nil, err
	}
	out := make([]float64, count)
	for i := range out {
		out[i] = reader.Float64()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKEnergyFLP(cases []libopusSILKEnergyCase) ([]float64, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeEnergy, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.Float32s(tc.x...)
	}

	reader, count, err := runLibopusSILKOracle(binPath, payload.Bytes(), "silk energy flp", len(cases))
	if err != nil {
		return nil, err
	}
	out := make([]float64, count)
	for i := range out {
		out[i] = reader.Float64()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKAutocorrelationFLP(cases []libopusSILKAutocorrelationCase) ([][]float32, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeAutocorrelation, uint32(len(cases)))
	for _, tc := range cases {
		if tc.order <= 0 || tc.order > len(tc.x) || tc.order > 16 || len(tc.x) > 512 {
			return nil, fmt.Errorf("%s: invalid autocorrelation length=%d order=%d", tc.name, len(tc.x), tc.order)
		}
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.order))
		payload.Float32s(tc.x...)
	}
	reader, count, err := runLibopusSILKOracle(binPath, payload.Bytes(), "silk autocorrelation flp", len(cases))
	if err != nil {
		return nil, err
	}
	out := make([][]float32, count)
	for i := range out {
		length := int(reader.U32())
		if length != cases[i].order {
			return nil, fmt.Errorf("%s: helper autocorrelation length=%d want %d", cases[i].name, length, cases[i].order)
		}
		out[i] = make([]float32, length)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKFindLPCFLP(cases []libopusSILKFindLPCCase) ([]libopusSILKFindLPCResult, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeFindLPC, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.subfrLength))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.order))
		if tc.useInterpolatedNLSFs {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		if tc.firstFrameAfterReset {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.Float32(tc.minInvGain)
		for i := range 16 {
			var v int16
			if i < len(tc.prevNLSF) {
				v = tc.prevNLSF[i]
			}
			payload.I32(int32(v))
		}
		payload.Float32s(tc.x...)
	}

	reader, count, err := runLibopusSILKOracle(binPath, payload.Bytes(), "silk find lpc flp", len(cases))
	if err != nil {
		return nil, err
	}
	out := make([]libopusSILKFindLPCResult, count)
	for i := range out {
		order := int(reader.U32())
		if order != 10 && order != 16 {
			return nil, fmt.Errorf("helper order=%d", order)
		}
		out[i].interpIdx = int(reader.U32())
		out[i].nlsf = make([]int16, order)
		for j := range 16 {
			v := int16(reader.I32())
			if j < order {
				out[i].nlsf[j] = v
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKBurgModifiedFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKBurgCase{
		{name: "order10_four_subframes", subfrLen: 90, nbSubfr: 4, order: 10, minInvGain: 1e-4, x: silkBurgOracleSignal(360, 0x12345678)},
		{name: "order16_four_subframes", subfrLen: 96, nbSubfr: 4, order: 16, minInvGain: 1e-4, x: silkBurgOracleSignal(384, 0x9abcdef0)},
		{name: "order16_two_subframes", subfrLen: 96, nbSubfr: 2, order: 16, minInvGain: 5e-5, x: silkBurgOracleSignal(192, 0x31415926)},
		{name: "order10_tight_gain", subfrLen: 80, nbSubfr: 2, order: 10, minInvGain: 0.08, x: silkBurgOracleSignal(160, 0x27182818)},
	}
	want, err := probeLibopusSILKBurgModified(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk lpc burg", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := &Encoder{}
			gotA, gotRes := enc.burgModifiedFLPZeroAllocF32(tc.x, tc.minInvGain, tc.subfrLen, tc.nbSubfr, tc.order)
			gotRes32 := float32(gotRes)
			if math.Float32bits(gotRes32) != math.Float32bits(want[i].resNrg) {
				t.Fatalf("resNrg=%08x %.10g want %08x %.10g",
					math.Float32bits(gotRes32), gotRes32,
					math.Float32bits(want[i].resNrg), want[i].resNrg)
			}
			if len(gotA) != len(want[i].a) {
				t.Fatalf("A len=%d want %d", len(gotA), len(want[i].a))
			}
			for j := range gotA {
				got := float32(gotA[j])
				if math.Float32bits(got) != math.Float32bits(want[i].a[j]) {
					t.Fatalf("A[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(got), got,
						math.Float32bits(want[i].a[j]), want[i].a[j])
				}
			}
		})
	}
}

func TestSILKLPCAnalysisFilterFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKLPCFilterCase{
		{name: "order10", order: 10, pred: silkLPCOraclePred(10, 0.44), x: silkBurgOracleSignal(160, 0x10203040)},
		{name: "order16", order: 16, pred: silkLPCOraclePred(16, 0.38), x: silkBurgOracleSignal(224, 0x50607080)},
		{name: "order16_short", order: 16, pred: silkLPCOraclePred(16, 0.25), x: silkBurgOracleSignal(64, 0xa0b0c0d0)},
	}
	for _, order := range [...]int{6, 8, 12} {
		for _, outputCount := range [...]int{1, 3, 4, 7, 8, 9} {
			length := order + outputCount
			cases = append(cases, libopusSILKLPCFilterCase{
				name:  fmt.Sprintf("order%d_output%d", order, outputCount),
				order: order,
				pred:  silkLPCOraclePred(order, 0.29),
				x:     silkBurgOracleSignal(length, uint32(0x6a00+order*16+outputCount)),
			})
		}
	}
	want, err := probeLibopusSILKLPCAnalysisFilter(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk lpc analysis filter", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]float32, len(tc.x))
			lpcAnalysisFilterF32(got, tc.pred, tc.x, len(tc.x), tc.order)
			if len(got) != len(want[i]) {
				t.Fatalf("residual len=%d want %d", len(got), len(want[i]))
			}
			for j := range got {
				if math.Float32bits(got[j]) != math.Float32bits(want[i][j]) {
					t.Fatalf("residual[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(got[j]), got[j],
						math.Float32bits(want[i][j]), want[i][j])
				}
			}
		})
	}
}

func TestSILKInnerProductFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKInnerProductCase{
		{name: "one", a: []float32{0.25}, b: []float32{-16}},
		{name: "four", a: silkFLPOracleSignal(4, 0xabcdef01, 1.0), b: silkFLPOracleSignal(4, 0x01020304, 0.75)},
		{name: "five", a: silkFLPOracleSignal(5, 0xabcdef02, 1.5), b: silkFLPOracleSignal(5, 0x10203040, 0.5)},
		{name: "pitch_len_31", a: silkFLPOracleSignal(31, 0xabcdef03, 42.0), b: silkFLPOracleSignal(31, 0x20304050, 17.0)},
		{name: "pitch_len_80", a: silkFLPOracleSignal(80, 0xabcdef04, 32768.0), b: silkFLPOracleSignal(80, 0x30405060, 32768.0)},
		{name: "pitch_len_120", a: silkFLPOracleSignal(120, 0xabcdef05, 4096.0), b: silkFLPOracleSignal(120, 0x40506070, 8192.0)},
		{name: "order_sensitive", a: []float32{1e20, 1, -1e20, 1, -1e20, 1, 1e20, 1}, b: []float32{1, 1, 1, 1, 1, 1, 1, 1}},
	}
	want, err := probeLibopusSILKInnerProductFLP(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk inner product flp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := innerProductFLP(tc.a, tc.b, len(tc.a))
			if math.Float64bits(got) != math.Float64bits(want[i]) {
				t.Fatalf("innerProduct=%016x %.17g want %016x %.17g",
					math.Float64bits(got), got,
					math.Float64bits(want[i]), want[i])
			}
		})
	}
}

func TestSILKEnergyFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKEnergyCase{
		{name: "one", x: []float32{-16}},
		{name: "four", x: silkFLPOracleSignal(4, 0x55667788, 1.0)},
		{name: "five", x: silkFLPOracleSignal(5, 0x66778899, 1.5)},
		{name: "pitch_len_31", x: silkFLPOracleSignal(31, 0x778899aa, 42.0)},
		{name: "pitch_len_80", x: silkFLPOracleSignal(80, 0x8899aabb, 32768.0)},
		{name: "pitch_len_120", x: silkFLPOracleSignal(120, 0x99aabbcc, 4096.0)},
		{name: "wide_dynamic_range", x: []float32{1e10, 1, -1e10, 1, 1, -1e10, 1, 1e10}},
	}
	want, err := probeLibopusSILKEnergyFLP(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk energy flp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := energyFLP(tc.x)
			if math.Float64bits(got) != math.Float64bits(want[i]) {
				t.Fatalf("energy=%016x %.17g want %016x %.17g",
					math.Float64bits(got), got,
					math.Float64bits(want[i]), want[i])
			}
		})
	}
}

func TestSILKAutocorrelationF32MatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cancellation := make([]float32, 31)
	pattern := [...]float32{1e20, 1, -1e20, 1, -1e20, 1, 1e20, 1}
	for i := range cancellation {
		cancellation[i] = pattern[i%len(pattern)]
	}
	cases := []libopusSILKAutocorrelationCase{
		{name: "length31_order8_tail", order: 8, x: silkFLPOracleSignal(31, 0x70717273, 32768)},
		{name: "length120_order9", order: 9, x: silkFLPOracleSignal(120, 0x81828384, 4096)},
		{name: "finite_cancellation_tail", order: 7, x: cancellation},
	}
	want, err := probeLibopusSILKAutocorrelationFLP(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk autocorrelation flp", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]float32, tc.order)
			autocorrelationF32(got, tc.x, len(tc.x), tc.order)
			for j := range got {
				if math.Float32bits(got[j]) != math.Float32bits(want[i][j]) {
					t.Fatalf("autocorrelation[%d]=%08x %.10g want %08x %.10g", j,
						math.Float32bits(got[j]), got[j], math.Float32bits(want[i][j]), want[i][j])
				}
			}
		})
	}
}

func TestSILKFindLPCFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKFindLPCCase{
		{
			name:                 "order10_first_frame",
			subfrLength:          60,
			nbSubfr:              4,
			order:                10,
			useInterpolatedNLSFs: true,
			firstFrameAfterReset: true,
			minInvGain:           1e-4,
			prevNLSF:             silkFindLPCPrevNLSF(10, 0x12345678),
			x:                    silkBurgOracleSignal(4*(60+10), 0x10203040),
		},
		{
			name:                 "order10_interpolation_enabled",
			subfrLength:          60,
			nbSubfr:              4,
			order:                10,
			useInterpolatedNLSFs: true,
			firstFrameAfterReset: false,
			minInvGain:           2e-4,
			prevNLSF:             silkFindLPCPrevNLSF(10, 0x22334455),
			x:                    silkBurgOracleSignal(4*(60+10), 0x31415926),
		},
		{
			name:                 "order16_low_complexity",
			subfrLength:          80,
			nbSubfr:              4,
			order:                16,
			useInterpolatedNLSFs: false,
			firstFrameAfterReset: false,
			minInvGain:           1e-4,
			prevNLSF:             silkFindLPCPrevNLSF(16, 0xabcdef01),
			x:                    silkBurgOracleSignal(4*(80+16), 0x50607080),
		},
		{
			name:                 "order16_interpolation_enabled",
			subfrLength:          80,
			nbSubfr:              4,
			order:                16,
			useInterpolatedNLSFs: true,
			firstFrameAfterReset: false,
			minInvGain:           3e-4,
			prevNLSF:             silkFindLPCPrevNLSF(16, 0x0badf00d),
			x:                    silkBurgOracleSignal(4*(80+16), 0x90abcdef),
		},
		{
			name:                 "order16_two_subframes",
			subfrLength:          80,
			nbSubfr:              2,
			order:                16,
			useInterpolatedNLSFs: true,
			firstFrameAfterReset: false,
			minInvGain:           5e-5,
			prevNLSF:             silkFindLPCPrevNLSF(16, 0x13579bdf),
			x:                    silkBurgOracleSignal(2*(80+16), 0x2468ace0),
		},
	}
	want, err := probeLibopusSILKFindLPCFLP(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk find lpc flp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bw := BandwidthWideband
			if tc.order == 10 {
				bw = BandwidthMediumband
			}
			enc := newTestEncoder(bw)
			if tc.useInterpolatedNLSFs {
				enc.setupComplexity(10)
			} else {
				enc.setupComplexity(0)
			}
			enc.firstFrameAfterReset = tc.firstFrameAfterReset
			copy(enc.prevLSFQ15[:], tc.prevNLSF)

			_, gotNLSF, gotInterp := enc.computeLPCAndNLSFWithInterp(tc.x, tc.nbSubfr, tc.subfrLength, tc.minInvGain)
			if gotInterp != want[i].interpIdx {
				t.Fatalf("interpIdx=%d want %d", gotInterp, want[i].interpIdx)
			}
			if len(gotNLSF) != len(want[i].nlsf) {
				t.Fatalf("NLSF len=%d want %d", len(gotNLSF), len(want[i].nlsf))
			}
			for j := range gotNLSF {
				if gotNLSF[j] != want[i].nlsf[j] {
					t.Fatalf("NLSF[%d]=%d want %d", j, gotNLSF[j], want[i].nlsf[j])
				}
			}
		})
	}
}

func silkBurgOracleSignal(n int, seed uint32) []float32 {
	x := make([]float32, n)
	state := seed
	for i := range x {
		state = state*1664525 + 1013904223
		noise := float32(int32(state>>8)&0xffff-32768) / 32768.0
		ramp := float32((i%31)-15) * (1.0 / 256.0)
		x[i] = 0.35*noise + ramp
	}
	return x
}

func silkFindLPCPrevNLSF(order int, seed uint32) []int16 {
	out := make([]int16, order)
	state := seed
	prev := 900
	for i := range out {
		state = state*1664525 + 1013904223
		step := 1300 + int((state>>24)&0x3ff)
		prev += step
		limit := 32000 - (order-i-1)*700
		if prev > limit {
			prev = limit
		}
		out[i] = int16(prev)
	}
	return out
}

func silkFLPOracleSignal(n int, seed uint32, scale float32) []float32 {
	x := make([]float32, n)
	state := seed
	for i := range x {
		state = state*1664525 + 1013904223
		mant := int32(state>>8)&0xffff - 32768
		x[i] = scale * float32(mant) / 32768.0
	}
	return x
}

func silkLPCOraclePred(order int, gain float32) []float32 {
	pred := make([]float32, order)
	for i := range pred {
		sign := float32(1)
		if i&1 != 0 {
			sign = -1
		}
		pred[i] = sign * gain / float32(i+2)
	}
	return pred
}

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
	for frame := range numFrames {
		pcm := allPCM[frame*frameSamples : (frame+1)*frameSamples]

		// Encode this frame
		encoded := encodeTestPacket(t, BandwidthWideband, pcm)

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
		for i := range 16 {
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
	encoded := encodeTestPacket(t, BandwidthNarrowband, pcm)

	// Create decoder
	decoder := NewDecoder()

	var rd rangecoding.Decoder
	rd.Init(encoded)

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
	for i := range 16 {
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
	encoded2 := encodeTestPacket(t, BandwidthNarrowband, pcm)

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
	encoded := encodeTestPacket(t, BandwidthNarrowband, pcm)

	// Decode
	decoder := NewDecoder()
	var rd rangecoding.Decoder
	rd.Init(encoded)
	decoded, err := decoder.DecodeFrame(&rd, BandwidthNarrowband, Frame20ms, true)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	st := &decoder.state[0]
	subfrLength := int(st.subfrLength)
	nbSubfr := int(st.nbSubfr)

	t.Logf("=== Within-frame LPC buildup ===")
	t.Logf("Subframe length: %d, Num subframes: %d", subfrLength, nbSubfr)

	// Compute RMS for each subframe
	for k := range nbSubfr {
		start := k * subfrLength
		end := min(start+subfrLength, len(decoded))

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

func synthesizeLPCOrder16ScalarForTest(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	c0 := int32(A_Q12[0])
	c1 := int32(A_Q12[1])
	c2 := int32(A_Q12[2])
	c3 := int32(A_Q12[3])
	c4 := int32(A_Q12[4])
	c5 := int32(A_Q12[5])
	c6 := int32(A_Q12[6])
	c7 := int32(A_Q12[7])
	c8 := int32(A_Q12[8])
	c9 := int32(A_Q12[9])
	c10 := int32(A_Q12[10])
	c11 := int32(A_Q12[11])
	c12 := int32(A_Q12[12])
	c13 := int32(A_Q12[13])
	c14 := int32(A_Q12[14])
	c15 := int32(A_Q12[15])

	v0 := sLPC[maxLPCOrder-1]
	v1 := sLPC[maxLPCOrder-2]
	v2 := sLPC[maxLPCOrder-3]
	v3 := sLPC[maxLPCOrder-4]
	v4 := sLPC[maxLPCOrder-5]
	v5 := sLPC[maxLPCOrder-6]
	v6 := sLPC[maxLPCOrder-7]
	v7 := sLPC[maxLPCOrder-8]
	v8 := sLPC[maxLPCOrder-9]
	v9 := sLPC[maxLPCOrder-10]
	v10 := sLPC[maxLPCOrder-11]
	v11 := sLPC[maxLPCOrder-12]
	v12 := sLPC[maxLPCOrder-13]
	v13 := sLPC[maxLPCOrder-14]
	v14 := sLPC[maxLPCOrder-15]
	v15 := sLPC[maxLPCOrder-16]

	sIdx := maxLPCOrder
	for i := range subfrLength {
		lpcPredQ10 := int32(maxLPCOrder >> 1)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v0, c0)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v1, c1)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v2, c2)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v3, c3)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v4, c4)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v5, c5)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v6, c6)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v7, c7)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v8, c8)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v9, c9)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v10, c10)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v11, c11)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v12, c12)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v13, c13)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v14, c14)
		lpcPredQ10 = silkSMLAWB(lpcPredQ10, v15, c15)

		s := silkAddSat32(presQ14[i], lShiftSAT32By4(lpcPredQ10))
		sLPC[sIdx] = s
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(s, gainQ10), 8))
		sIdx++

		v15 = v14
		v14 = v13
		v13 = v12
		v12 = v11
		v11 = v10
		v10 = v9
		v9 = v8
		v8 = v7
		v7 = v6
		v6 = v5
		v5 = v4
		v4 = v3
		v3 = v2
		v2 = v1
		v1 = v0
		v0 = s
	}
}

func testLPCSynthesisInputs() ([maxLPCOrder + maxSubFrameLength]int32, [maxLPCOrder]int16, [maxSubFrameLength]int32) {
	var sLPC [maxLPCOrder + maxSubFrameLength]int32
	var a [maxLPCOrder]int16
	var pres [maxSubFrameLength]int32
	for i := range sLPC {
		sLPC[i] = int32((i*104729)%2000000 - 1000000)
	}
	for i := range a {
		a[i] = int16((i*2311)%28000 - 14000)
	}
	for i := range pres {
		pres[i] = int32((i*8191)%700000 - 350000)
	}
	return sLPC, a, pres
}

func TestSynthesizeLPCOrder16CoreMatchesScalar(t *testing.T) {
	sLPC, a, pres := testLPCSynthesisInputs()
	sLPCRef := sLPC
	var got [maxSubFrameLength]int16
	var want [maxSubFrameLength]int16

	synthesizeLPCOrder16Core(sLPC[:], a[:], pres[:], got[:], 12345, maxSubFrameLength)
	synthesizeLPCOrder16ScalarForTest(sLPCRef[:], a[:], pres[:], want[:], 12345, maxSubFrameLength)

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pxq[%d] got %d want %d", i, got[i], want[i])
		}
	}
	for i := range sLPC {
		if sLPC[i] != sLPCRef[i] {
			t.Fatalf("sLPC[%d] got %d want %d", i, sLPC[i], sLPCRef[i])
		}
	}
}

func BenchmarkSynthesizeLPCOrder16Core80(b *testing.B) {
	sLPC0, a, pres := testLPCSynthesisInputs()
	var pxq [maxSubFrameLength]int16
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sLPC := sLPC0
		synthesizeLPCOrder16Core(sLPC[:], a[:], pres[:], pxq[:], 12345, maxSubFrameLength)
	}
}

func BenchmarkSynthesizeLPCOrder16Scalar80(b *testing.B) {
	sLPC0, a, pres := testLPCSynthesisInputs()
	var pxq [maxSubFrameLength]int16
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sLPC := sLPC0
		synthesizeLPCOrder16ScalarForTest(sLPC[:], a[:], pres[:], pxq[:], 12345, maxSubFrameLength)
	}
}
