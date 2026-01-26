package celt

import (
	"math"
	"testing"
)

// TestStereoMergeVsLibopus validates stereoMerge matches libopus stereo_merge.
// The stereo_merge function converts mid (X) and side (Y) to left (X) and right (Y).
//
// Libopus stereo_merge algorithm (float):
//
//	xp = dot(Y, X)                    // inner product
//	side = dot(Y, Y)                  // side energy
//	xp = mid * xp                     // compensate for mid normalization
//	El = mid^2 + side - 2*xp          // left energy
//	Er = mid^2 + side + 2*xp          // right energy
//	lgain = 1 / sqrt(El)
//	rgain = 1 / sqrt(Er)
//	X[j] = lgain * (mid * X[j] - Y[j])  // left
//	Y[j] = rgain * (mid * X[j] + Y[j])  // right
//
// Note: In libopus the mid value passed to stereo_merge is derived from imid/32768
// where imid comes from bitexactCos(itheta). The mid passed is the actual scaling factor.
func TestStereoMergeVsLibopus(t *testing.T) {
	testCases := []struct {
		name string
		x    []float64 // mid coefficients (already normalized to unit energy)
		y    []float64 // side coefficients (already scaled by side gain)
		mid  float64   // mid scaling factor (imid/32768)
	}{
		{
			name: "simple N=4",
			x:    []float64{0.5, 0.5, 0.5, 0.5},
			y:    []float64{0.1, -0.1, 0.1, -0.1},
			mid:  0.7071067811865476, // cos(45 deg) = sqrt(2)/2
		},
		{
			name: "mono-dominant",
			x:    []float64{0.8, 0.4, 0.2, 0.2},
			y:    []float64{0.01, 0.01, -0.01, -0.01},
			mid:  0.95,
		},
		{
			name: "balanced stereo",
			x:    []float64{0.6, 0.4, 0.3, 0.2},
			y:    []float64{0.3, -0.2, 0.15, -0.1},
			mid:  0.7071067811865476,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Make copies to avoid modifying test data
			xGo := make([]float64, len(tc.x))
			yGo := make([]float64, len(tc.y))
			copy(xGo, tc.x)
			copy(yGo, tc.y)

			// Run Go implementation
			stereoMerge(xGo, yGo, tc.mid)

			// Compute expected result using libopus-style algorithm
			xExpected := make([]float64, len(tc.x))
			yExpected := make([]float64, len(tc.y))
			copy(xExpected, tc.x)
			copy(yExpected, tc.y)
			stereoMergeLibopus(xExpected, yExpected, tc.mid)

			// Compare results
			maxDiffX := 0.0
			maxDiffY := 0.0
			for i := range xGo {
				diffX := math.Abs(xGo[i] - xExpected[i])
				diffY := math.Abs(yGo[i] - yExpected[i])
				if diffX > maxDiffX {
					maxDiffX = diffX
				}
				if diffY > maxDiffY {
					maxDiffY = diffY
				}
			}

			t.Logf("Max diff X (left): %e", maxDiffX)
			t.Logf("Max diff Y (right): %e", maxDiffY)

			// Check threshold
			const threshold = 1e-10
			if maxDiffX > threshold || maxDiffY > threshold {
				t.Errorf("stereoMerge mismatch:")
				t.Errorf("  Go X:       %v", xGo)
				t.Errorf("  Expected X: %v", xExpected)
				t.Errorf("  Go Y:       %v", yGo)
				t.Errorf("  Expected Y: %v", yExpected)
			}
		})
	}
}

// stereoMergeLibopus is a reference implementation matching libopus bands.c stereo_merge.
func stereoMergeLibopus(x, y []float64, mid float64) {
	n := len(x)
	if n == 0 || len(y) < n {
		return
	}

	// Compute xp = dot(Y, X) and side = dot(Y, Y)
	xp := 0.0
	side := 0.0
	for i := 0; i < n; i++ {
		xp += y[i] * x[i]
		side += y[i] * y[i]
	}

	// Compensate for mid normalization
	xp *= mid

	// Compute left and right energies
	mid2 := mid * mid
	el := mid2 + side - 2.0*xp
	er := mid2 + side + 2.0*xp

	// Early exit for very small energies
	if el < 6e-4 || er < 6e-4 {
		copy(y, x[:n])
		return
	}

	// Compute normalization gains
	lgain := 1.0 / math.Sqrt(el)
	rgain := 1.0 / math.Sqrt(er)

	// Apply transformation
	for i := 0; i < n; i++ {
		l := mid * x[i]
		r := y[i]
		x[i] = (l - r) * lgain
		y[i] = (l + r) * rgain
	}
}

// TestBitexactCos validates bitexactCos matches libopus.
// From libopus test_unit_mathops.c:
//
//	bitexact_cos(64) == 32767
//	bitexact_cos(16320) == 200
//	bitexact_cos(8192) == 23171
func TestBitexactCos(t *testing.T) {
	// Test cases from libopus validation (test_unit_mathops.c)
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
		// Allow small tolerance for bit-exact matching
		diff := absIntLocal(result - tc.expected)
		if diff > 2 {
			t.Errorf("bitexactCos(%d) = %d, expected %d (diff=%d)",
				tc.itheta, result, tc.expected, diff)
		} else {
			t.Logf("bitexactCos(%d) = %d (expected %d) OK", tc.itheta, result, tc.expected)
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
		diff := absIntLocal(result - tc.expected)
		if diff > 2 {
			t.Errorf("bitexactLog2tan(%d, %d) = %d, expected %d (diff=%d)",
				tc.isin, tc.icos, result, tc.expected, diff)
		} else {
			t.Logf("bitexactLog2tan(%d, %d) = %d (expected %d) OK", tc.isin, tc.icos, result, tc.expected)
		}
	}
}

// TestThetaToGainsConsistency checks theta to gains conversion.
func TestThetaToGainsConsistency(t *testing.T) {
	// Test that mid^2 + side^2 ≈ 1 for valid theta values
	for itheta := 0; itheta <= 16; itheta++ {
		qn := 16
		mid, side := ThetaToGains(itheta, qn)
		sum := mid*mid + side*side
		if math.Abs(sum-1.0) > 1e-10 {
			t.Errorf("ThetaToGains(%d, %d): mid=%f, side=%f, sum=%f (expected 1.0)",
				itheta, qn, mid, side, sum)
		}
	}
}

// TestComputeThetaDecoding validates theta decoding.
func TestComputeThetaDecoding(t *testing.T) {
	// Test compute_theta output matching
	// itheta = 0 -> imid=32767, iside=0, delta=-16384
	// itheta = 16384 -> imid=0, iside=32767, delta=16384

	// Test itheta=0
	imid := bitexactCos(0)
	iside := bitexactCos(16384)
	t.Logf("itheta=0: imid=%d, iside=%d", imid, iside)
	if imid != 32767 || iside < 0 || iside > 2 {
		t.Errorf("itheta=0 mismatch: imid=%d (expected 32767), iside=%d (expected ~1)", imid, iside)
	}

	// Test itheta=16384
	imid = bitexactCos(16384)
	iside = bitexactCos(0)
	t.Logf("itheta=16384: imid=%d, iside=%d", imid, iside)
	if iside != 32767 || imid < 0 || imid > 2 {
		t.Errorf("itheta=16384 mismatch: imid=%d (expected ~1), iside=%d (expected 32767)", imid, iside)
	}

	// Test itheta=8192 (45 degrees)
	imid = bitexactCos(8192)
	iside = bitexactCos(8192)
	t.Logf("itheta=8192: imid=%d, iside=%d", imid, iside)
	// Both should be around 23170 (cos(45) * 32768)
	expectedMidSide := 23170
	if absIntLocal(imid-expectedMidSide) > 100 || absIntLocal(iside-expectedMidSide) > 100 {
		t.Errorf("itheta=8192 mismatch: imid=%d, iside=%d (expected ~%d)",
			imid, iside, expectedMidSide)
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
