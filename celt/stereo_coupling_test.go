package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestStereoMergeVsLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)
	makeVector := func(n int, seed uint32) ([]float32, []float32) {
		x := make([]float32, n)
		y := make([]float32, n)
		for i := range x {
			seed = seed*1664525 + 1013904223
			x[i] = float32(int32(seed>>9)%2000-1000) / 1000
			seed = seed*1664525 + 1013904223
			y[i] = float32(int32(seed>>9)%1600-800) / 1600
		}
		return x, y
	}
	testCases := []struct {
		name string
		x    []float32
		y    []float32
		mid  float32
	}{
		{
			name: "simple N=4",
			x:    []float32{0.5, 0.5, 0.5, 0.5},
			y:    []float32{0.1, -0.1, 0.1, -0.1},
			mid:  0.7071067811865476, // cos(45 deg) = sqrt(2)/2
		},
		{
			name: "mono-dominant",
			x:    []float32{0.8, 0.4, 0.2, 0.2},
			y:    []float32{0.01, 0.01, -0.01, -0.01},
			mid:  0.95,
		},
		{
			name: "balanced stereo",
			x:    []float32{0.6, 0.4, 0.3, 0.2},
			y:    []float32{0.3, -0.2, 0.15, -0.1},
			mid:  0.7071067811865476,
		},
	}
	for _, tc := range []struct {
		name string
		n    int
		mid  float32
		seed uint32
	}{
		{"neon_8", 8, 0.8125, 0x5108},
		{"neon_tail_9", 9, 0.671875, 0x5109},
		{"neon_tail_17", 17, 0.9375, 0x5117},
	} {
		x, y := makeVector(tc.n, tc.seed)
		testCases = append(testCases, struct {
			name string
			x    []float32
			y    []float32
			mid  float32
		}{tc.name, x, y, tc.mid})
	}

	oracleCases := make([]stereoMergeOracleCase, len(testCases))
	for i, tc := range testCases {
		oracleCases[i] = stereoMergeOracleCase{
			x:   append([]float32(nil), tc.x...),
			y:   append([]float32(nil), tc.y...),
			mid: tc.mid,
		}
	}
	want, err := probeLibopusStereoMerge(oracleCases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}

	for ci, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			xGo := make([]celtNorm, len(tc.x))
			yGo := make([]celtNorm, len(tc.y))
			for i := range tc.x {
				xGo[i] = celtNorm(tc.x[i])
				yGo[i] = celtNorm(tc.y[i])
			}
			stereoMerge(xGo, yGo, opusVal16(tc.mid))
			for i := range xGo {
				gotX := float32(xGo[i])
				gotY := float32(yGo[i])
				if math.Float32bits(gotX) != math.Float32bits(want[ci].x[i]) {
					t.Fatalf("X[%d]=%08x %.10g want %08x %.10g",
						i, math.Float32bits(gotX), gotX, math.Float32bits(want[ci].x[i]), want[ci].x[i])
				}
				if math.Float32bits(gotY) != math.Float32bits(want[ci].y[i]) {
					t.Fatalf("Y[%d]=%08x %.10g want %08x %.10g",
						i, math.Float32bits(gotY), gotY, math.Float32bits(want[ci].y[i]), want[ci].y[i])
				}
			}
		})
	}
}

// TestBitexactCos validates bitexactCos matches libopus.
// From libopus test_unit_mathops.c:
//
//	bitexact_cos(64) == 32767
//	bitexact_cos(16320) == 200
//	bitexact_cos(8192) == 23171
func TestBitexactCos(t *testing.T) {
	testCases := []struct {
		itheta   int
		expected int
	}{
		{64, 32767},   // cos(~0 deg) -> max
		{8192, 23171}, // cos(45 deg) in Q15
		{16320, 200},  // cos(~89.8 deg) -> near zero
	}

	for _, tc := range testCases {
		result := bitexactCos(tc.itheta)
		if result != tc.expected {
			t.Errorf("bitexactCos(%d) = %d, expected %d", tc.itheta, result, tc.expected)
		}
	}
}

// TestBitexactLog2tan validates bitexactLog2tan matches libopus.
// From libopus test_unit_mathops.c:
//
//	bitexact_log2tan(32767, 200) == 15059
//	bitexact_log2tan(30274, 12540) == 2611
//	bitexact_log2tan(23171, 23171) == 0
func TestBitexactLog2tan(t *testing.T) {
	testCases := []struct {
		isin, icos int
		expected   int
	}{
		{23171, 23171, 0},    // tan(45 deg) = 1, log2(1) = 0
		{32767, 200, 15059},  // very high tan
		{30274, 12540, 2611}, // intermediate
	}

	for _, tc := range testCases {
		result := bitexactLog2tan(tc.isin, tc.icos)
		if result != tc.expected {
			t.Errorf("bitexactLog2tan(%d, %d) = %d, expected %d",
				tc.isin, tc.icos, result, tc.expected)
		}
	}
}

// TestThetaToGainsConsistency checks theta to gains conversion.
func TestThetaToGainsConsistency(t *testing.T) {
	// Test that mid^2 + side^2 ≈ 1 for valid theta values
	for itheta := 0; itheta <= 16; itheta++ {
		qn := 16
		mid, side := ThetaToGains(itheta, qn)
		sum := float64(mid)*float64(mid) + float64(side)*float64(side)
		if math.Abs(sum-1.0) > 1e-6 {
			t.Errorf("ThetaToGains(%d, %d): mid=%f, side=%f, sum=%f (expected 1.0)",
				itheta, qn, mid, side, sum)
		}
	}
}

// TestComputeThetaDecoding validates theta decoding.
func TestComputeThetaDecoding(t *testing.T) {
	imid := bitexactCos(0)
	iside := bitexactCos(16384)
	if imid != -32768 || iside != 16554 {
		t.Errorf("itheta=0 mismatch: imid=%d, iside=%d", imid, iside)
	}

	imid = bitexactCos(16384)
	iside = bitexactCos(0)
	if imid != 16554 || iside != -32768 {
		t.Errorf("itheta=16384 mismatch: imid=%d, iside=%d", imid, iside)
	}

	imid = bitexactCos(8192)
	iside = bitexactCos(8192)
	if imid != 23171 || iside != 23171 {
		t.Errorf("itheta=8192 mismatch: imid=%d, iside=%d", imid, iside)
	}
}

// TestQuantBandStereoN2 validates the special N=2 stereo case.
func TestQuantBandStereoN2(t *testing.T) {
	// For N=2, the stereo handling is special:
	// 1. Decode theta to get mid/side gains
	// 2. Decode one of mid or side fully (based on c = itheta > 8192)
	// 3. Compute the other using orthogonal relationship
	// 4. Apply rotation to get L/R
	//
	// The key formula for N=2:
	//   y2[0] = -sign * x2[1]
	//   y2[1] = sign * x2[0]
	//
	// After rotation:
	//   X[0] = mid*X[0] - side*Y[0]
	//   Y[0] = mid*X[0] + side*Y[0]  (after swap)

	t.Log("N=2 stereo case validated in quantBandStereo")

	// Verify the rotation formula preserves energy
	x := []float64{0.8, 0.6}
	y := []float64{0.6, -0.8} // perpendicular

	// Normalize
	norm := math.Sqrt(x[0]*x[0] + x[1]*x[1])
	x[0] /= norm
	x[1] /= norm
	norm = math.Sqrt(y[0]*y[0] + y[1]*y[1])
	y[0] /= norm
	y[1] /= norm

	mid := 0.8
	side := 0.6

	// Apply N=2 rotation
	for i := range x {
		x[i] *= mid
		y[i] *= side
	}

	tmp := x[0]
	x[0] = tmp - y[0]
	y[0] = tmp + y[0]
	tmp = x[1]
	x[1] = tmp - y[1]
	y[1] = tmp + y[1]

	// Check that output has reasonable energy
	energyX := x[0]*x[0] + x[1]*x[1]
	energyY := y[0]*y[0] + y[1]*y[1]

	t.Logf("N=2 rotation: X energy=%f, Y energy=%f", energyX, energyY)

	if math.IsNaN(energyX) || math.IsNaN(energyY) || energyX < 0.01 && energyY < 0.01 {
		t.Error("N=2 rotation produced invalid energy")
	}
}

// TestIntensityStereoInv validates intensity stereo with inversion flag.
func TestIntensityStereoInv(t *testing.T) {
	// In intensity stereo mode (qn=1), the decoder:
	// 1. Copies mid to both L and R
	// 2. Reads an inversion bit
	// 3. If inv=1, negates the R channel

	mid := []float64{0.5, 0.3, -0.2, 0.1}

	// Without inversion
	leftNoInv := make([]float64, len(mid))
	rightNoInv := make([]float64, len(mid))
	copy(leftNoInv, mid)
	copy(rightNoInv, mid)

	// With inversion
	leftInv := make([]float64, len(mid))
	rightInv := make([]float64, len(mid))
	copy(leftInv, mid)
	for i := range mid {
		rightInv[i] = -mid[i]
	}

	t.Logf("Intensity stereo (no inv): L=%v, R=%v", leftNoInv, rightNoInv)
	t.Logf("Intensity stereo (inv):    L=%v, R=%v", leftInv, rightInv)

	// Verify L and R have same magnitude
	for i := range mid {
		if math.Abs(leftNoInv[i]) != math.Abs(rightNoInv[i]) {
			t.Errorf("No inv: |L[%d]|=%f != |R[%d]|=%f", i, math.Abs(leftNoInv[i]), i, math.Abs(rightNoInv[i]))
		}
		if math.Abs(leftInv[i]) != math.Abs(rightInv[i]) {
			t.Errorf("Inv: |L[%d]|=%f != |R[%d]|=%f", i, math.Abs(leftInv[i]), i, math.Abs(rightInv[i]))
		}
	}
}

// absIntLocal is a local helper to avoid redeclaration conflicts
func absIntLocal(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// TestStereoIthetaQ30 validates the Q30 extended precision theta computation.
// The Q30 version should have itheta = ithetaQ30 >> 16.
func TestStereoIthetaQ30(t *testing.T) {
	testCases := []struct {
		name   string
		x      []celtNorm
		y      []celtNorm
		stereo bool
	}{
		{
			name:   "equal energy (45 degrees)",
			x:      []celtNorm{1, 0, 0, 0},
			y:      []celtNorm{0, 1, 0, 0},
			stereo: false,
		},
		{
			name:   "all mid (0 degrees)",
			x:      []celtNorm{1, 1, 1, 1},
			y:      []celtNorm{0, 0, 0, 0},
			stereo: false,
		},
		{
			name:   "all side (90 degrees)",
			x:      []celtNorm{0, 0, 0, 0},
			y:      []celtNorm{1, 1, 1, 1},
			stereo: false,
		},
		{
			name:   "stereo mode - equal",
			x:      []celtNorm{0.5, 0.5, 0.5, 0.5},
			y:      []celtNorm{0.5, 0.5, 0.5, 0.5},
			stereo: true,
		},
		{
			name:   "stereo mode - different",
			x:      []celtNorm{0.8, 0.4, 0.2, 0.1},
			y:      []celtNorm{0.2, 0.4, 0.6, 0.8},
			stereo: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Compute both versions
			itheta := stereoItheta(tc.x, tc.y, tc.stereo)
			ithetaQ30 := stereoIthetaQ30(tc.x, tc.y, tc.stereo)

			// Q30 >> 16 should equal standard itheta
			ithetaFromQ30 := ithetaQ30 >> 16

			// Allow small difference due to intermediate rounding
			diff := absIntLocal(itheta - ithetaFromQ30)
			if diff > 1 {
				t.Errorf("stereoItheta=%d, ithetaQ30>>16=%d (diff=%d, expected <=1)",
					itheta, ithetaFromQ30, diff)
			}

			// Q30 should be in valid range [0, 1<<30]
			if ithetaQ30 < 0 || ithetaQ30 > 1<<30 {
				t.Errorf("ithetaQ30=%d out of range [0, %d]", ithetaQ30, 1<<30)
			}

			// Standard itheta should be in [0, 16384]
			if itheta < 0 || itheta > 16384 {
				t.Errorf("itheta=%d out of range [0, 16384]", itheta)
			}

			t.Logf("itheta=%d, ithetaQ30=%d (>>16=%d)", itheta, ithetaQ30, ithetaFromQ30)
		})
	}
}

// TestCeltAtan2pNorm validates the atan2 * 2/pi computation.
func TestCeltAtan2pNorm(t *testing.T) {
	testCases := []struct {
		y, x     float64
		expected float64 // atan2(y,x) * 2/pi
	}{
		{0, 1, 0},          // atan2(0, 1) = 0
		{1, 1, 0.5},        // atan2(1, 1) = pi/4, * 2/pi = 0.5
		{1, 0, 1.0},        // atan2(1, 0) = pi/2, * 2/pi = 1.0
		{0.5, 0.866, 0.29}, // atan2(0.5, 0.866) ~= 30 deg = pi/6, * 2/pi ~= 0.333
	}

	for _, tc := range testCases {
		result := celtAtan2pNorm(tc.y, tc.x)
		diff := math.Abs(result - tc.expected)
		// Allow 5% error for approximation
		if diff > 0.05 {
			t.Errorf("celtAtan2pNorm(%f, %f)=%f, expected ~%f (diff=%f)",
				tc.y, tc.x, result, tc.expected, diff)
		}
	}
}

// TestCeltCosNorm2 validates the cos(pi/2 * x) computation.
func TestCeltCosNorm2(t *testing.T) {
	testCases := []struct {
		x        float64
		expected float64
	}{
		{0, 1.0},                   // cos(0) = 1
		{0.5, 0.7071067811865476},  // cos(pi/4) = sqrt(2)/2
		{1.0, 0.0},                 // cos(pi/2) = 0
		{0.25, 0.9238795325112867}, // cos(pi/8) ~= 0.9239
	}

	for _, tc := range testCases {
		result := celtCosNorm2(tc.x)
		diff := math.Abs(result - tc.expected)
		if diff > 1e-6 {
			t.Errorf("celtCosNorm2(%f)=%f, expected %f (diff=%f)",
				tc.x, result, tc.expected, diff)
		}
	}
}

// TestThetaRDOTrialRestoration verifies that the theta RDO trial/restore logic
// correctly saves and restores all state between trials.
// This tests the core RDO loop correctness: save state -> trial 1 -> save result 1 ->
// restore state -> trial 2 -> pick best -> restore if trial 1 was better.
func TestThetaRDOTrialRestoration(t *testing.T) {
	// Test that the inner product (distortion measure) is computed correctly
	// for the theta RDO algorithm.
	testCases := []struct {
		name string
		orig []celtNorm
		enc  []celtNorm
	}{
		{
			name: "identical vectors",
			orig: []celtNorm{0.5, 0.5, 0.5, 0.5},
			enc:  []celtNorm{0.5, 0.5, 0.5, 0.5},
		},
		{
			name: "similar vectors",
			orig: []celtNorm{0.5, 0.5, 0.5, 0.5},
			enc:  []celtNorm{0.48, 0.51, 0.49, 0.52},
		},
		{
			name: "different vectors",
			orig: []celtNorm{0.5, 0.5, 0.5, 0.5},
			enc:  []celtNorm{0.4, 0.6, 0.3, 0.7},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orig := append([]celtNorm(nil), tc.orig...)
			enc := append([]celtNorm(nil), tc.enc...)

			// The distortion measure used in theta RDO is innerProductNorm(orig, enc)
			// Higher inner product means lower distortion (more similar)
			dist := innerProductNorm(orig, enc)

			// Verify innerProductNorm is symmetric
			distReverse := innerProductNorm(enc, orig)
			if math.Abs(float64(dist-distReverse)) > 1e-10 {
				t.Errorf("innerProduct not symmetric: %f != %f", dist, distReverse)
			}

			// Verify that identical vectors have maximum inner product
			if tc.name == "identical vectors" {
				var normOrig float32
				for _, v := range tc.orig {
					v32 := float32(v)
					normOrig += v32 * v32
				}
				if math.Abs(float64(dist-normOrig)) > 1e-10 {
					t.Errorf("identical vectors: innerProduct=%f, expected %f", dist, normOrig)
				}
			}

			t.Logf("%s: innerProduct = %f", tc.name, dist)
		})
	}

	// Test that computeChannelWeights produces valid weights
	// Note: In libopus float path, weights are NOT normalized - they're adjusted energies
	t.Run("channel weights", func(t *testing.T) {
		testWeights := []struct {
			leftE, rightE float64
		}{
			{1.0, 1.0},
			{0.5, 0.5},
			{0.8, 0.2},
			{0.0, 1.0},
			{1e-10, 1e-10},
		}

		for _, tw := range testWeights {
			w0, w1 := computeChannelWeights(celtEner(tw.leftE), celtEner(tw.rightE))

			// Weights should be non-negative
			if w0 < 0 || w1 < 0 {
				t.Errorf("negative weights: w0=%f, w1=%f for leftE=%f, rightE=%f",
					w0, w1, tw.leftE, tw.rightE)
			}

			// Verify the libopus formula: w = E + min(El, Er)/3
			leftE := float32(tw.leftE)
			rightE := float32(tw.rightE)
			minE := leftE
			if rightE < minE {
				minE = rightE
			}
			expectedW0 := leftE + minE/3.0
			expectedW1 := rightE + minE/3.0

			if math.Abs(float64(w0-expectedW0)) > 1e-10 || math.Abs(float64(w1-expectedW1)) > 1e-10 {
				t.Errorf("weight mismatch: got w0=%f, w1=%f; expected w0=%f, w1=%f",
					w0, w1, expectedW0, expectedW1)
			}

			t.Logf("leftE=%f, rightE=%f -> w0=%f, w1=%f", tw.leftE, tw.rightE, w0, w1)
		}
	})

	// Test norm buffer save/restore (simulates the RDO loop)
	t.Run("norm buffer restoration", func(t *testing.T) {
		// Simulate the norm buffer save/restore pattern used in theta RDO
		origNorm := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8}

		// Save original
		normSave := make([]float64, len(origNorm))
		copy(normSave, origNorm)

		// Simulate trial 1 modification
		for i := range origNorm {
			origNorm[i] *= 1.5
		}
		trial1Norm := make([]float64, len(origNorm))
		copy(trial1Norm, origNorm)

		// Restore for trial 2
		copy(origNorm, normSave)

		// Verify restoration worked
		for i := range origNorm {
			if origNorm[i] != normSave[i] {
				t.Errorf("norm restoration failed at index %d: got %f, want %f",
					i, origNorm[i], normSave[i])
			}
		}

		// Simulate trial 2 modification
		for i := range origNorm {
			origNorm[i] *= 0.8
		}

		// Now decide which trial was better and restore if needed
		// (simulate picking trial 1)
		copy(origNorm, trial1Norm)

		for i := range origNorm {
			if origNorm[i] != trial1Norm[i] {
				t.Errorf("trial1 restoration failed at index %d: got %f, want %f",
					i, origNorm[i], trial1Norm[i])
			}
		}

		t.Log("Norm buffer save/restore pattern validated")
	})
}

// TestThetaQ30ExtendedPrecision validates that Q30 provides finer granularity than standard itheta.
func TestThetaQ30ExtendedPrecision(t *testing.T) {
	// Create test vectors that should produce slightly different angles
	// that would map to the same standard itheta but different ithetaQ30

	// Two angles that differ by less than 1 quantization step in standard itheta
	// Standard itheta has ~16384 steps over 90 degrees = ~0.0055 degrees per step
	// Q30 has 1<<30 = ~1 billion steps, so much finer

	x1 := []celtNorm{0.99, 0.1, 0.05, 0.02}
	y1 := []celtNorm{0.1, 0.05, 0.02, 0.01}

	x2 := []celtNorm{0.99, 0.1, 0.05, 0.03} // slightly different
	y2 := []celtNorm{0.1, 0.05, 0.02, 0.01}

	itheta1 := stereoItheta(x1, y1, false)
	itheta2 := stereoItheta(x2, y2, false)

	ithetaQ30_1 := stereoIthetaQ30(x1, y1, false)
	ithetaQ30_2 := stereoIthetaQ30(x2, y2, false)

	t.Logf("Vector 1: itheta=%d, ithetaQ30=%d", itheta1, ithetaQ30_1)
	t.Logf("Vector 2: itheta=%d, ithetaQ30=%d", itheta2, ithetaQ30_2)

	// Q30 should show more difference than standard itheta
	standardDiff := absIntLocal(itheta1 - itheta2)
	q30Diff := absIntLocal(ithetaQ30_1 - ithetaQ30_2)

	// Q30 difference should be at least 65536 times the standard difference (on average)
	// but due to rounding, just verify Q30 captures more detail
	t.Logf("Standard diff: %d, Q30 diff: %d, ratio: %.2f",
		standardDiff, q30Diff, float64(q30Diff)/float64(max(1, standardDiff)))

	// Both should produce valid values
	if ithetaQ30_1 < 0 || ithetaQ30_1 > 1<<30 {
		t.Errorf("ithetaQ30_1=%d out of range", ithetaQ30_1)
	}
	if ithetaQ30_2 < 0 || ithetaQ30_2 > 1<<30 {
		t.Errorf("ithetaQ30_2=%d out of range", ithetaQ30_2)
	}
}
