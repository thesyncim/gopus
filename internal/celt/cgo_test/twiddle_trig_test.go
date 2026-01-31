// Package cgo provides CGO comparison tests for FFT twiddle and MDCT trig tables.
// This file verifies that Go implementations match libopus float32 precision exactly.
package cgo

import (
	"math"
	"testing"
)

// LibopusPi is the PI constant used in libopus celt/mathops.h
const LibopusPi = 3.1415926535897931

// TestFFTTwiddles_GoVsLibopus compares Go FFT twiddle computation against libopus.
// Reference: kiss_fft.c lines 427-431 (float path)
func TestFFTTwiddles_GoVsLibopus(t *testing.T) {
	// Test common FFT sizes used in CELT
	// N/4 complex FFT sizes for MDCT: 480, 240, 120, 60
	fftSizes := []int{480, 240, 120, 60, 30, 15}

	for _, nfft := range fftSizes {
		t.Run(formatTestName("nfft=%d", nfft), func(t *testing.T) {
			// Get libopus twiddles
			libR, libI := ComputeLibopusFFTTwiddles(nfft)

			// Compute Go twiddles matching libopus formula exactly
			goR := make([]float32, nfft)
			goI := make([]float32, nfft)
			const pi = 3.14159265358979323846264338327
			for i := 0; i < nfft; i++ {
				phase := (-2.0 * pi / float64(nfft)) * float64(i)
				goR[i] = float32(math.Cos(phase))
				goI[i] = float32(math.Sin(phase))
			}

			// Compare
			var maxDiffR, maxDiffI float64
			var maxIdxR, maxIdxI int
			var mismatchCount int

			for i := 0; i < nfft; i++ {
				diffR := math.Abs(float64(libR[i]) - float64(goR[i]))
				diffI := math.Abs(float64(libI[i]) - float64(goI[i]))

				if diffR > maxDiffR {
					maxDiffR = diffR
					maxIdxR = i
				}
				if diffI > maxDiffI {
					maxDiffI = diffI
					maxIdxI = i
				}

				// Check for exact bit match
				if libR[i] != goR[i] || libI[i] != goI[i] {
					mismatchCount++
					if mismatchCount <= 5 {
						t.Logf("  Mismatch at i=%d: libR=%.9e, goR=%.9e, diffR=%.2e",
							i, libR[i], goR[i], diffR)
						t.Logf("                   : libI=%.9e, goI=%.9e, diffI=%.2e",
							libI[i], goI[i], diffI)
					}
				}
			}

			t.Logf("nfft=%d: maxDiffR=%.2e at idx %d, maxDiffI=%.2e at idx %d, mismatches=%d/%d",
				nfft, maxDiffR, maxIdxR, maxDiffI, maxIdxI, mismatchCount, nfft)

			// For float32, we expect bit-exact match since both use same formula
			if mismatchCount > 0 {
				t.Errorf("FFT twiddles have %d mismatches out of %d values", mismatchCount, nfft)
			}
		})
	}
}

// TestMDCTTrig_GoVsLibopus compares Go MDCT trig computation against libopus.
// Reference: mdct.c lines 100-101 (float path)
func TestMDCTTrig_GoVsLibopus(t *testing.T) {
	// Test MDCT sizes used in CELT
	// N = 2 * frameSize: 1920, 960, 480, 240
	mdctSizes := []int{1920, 960, 480, 240}

	for _, N := range mdctSizes {
		t.Run(formatTestName("N=%d", N), func(t *testing.T) {
			N2 := N / 2

			// Get libopus trig table
			libTrig := ComputeLibopusMDCTTrig(N)

			// Compute Go trig table matching libopus formula exactly
			// Must use LibopusPi (3.1415926535897931) not math.Pi
			goTrig := make([]float32, N2)
			for i := 0; i < N2; i++ {
				angle := 2.0 * LibopusPi * (float64(i) + 0.125) / float64(N)
				goTrig[i] = float32(math.Cos(angle))
			}

			// Compare
			var maxDiff float64
			var maxIdx int
			var mismatchCount int

			for i := 0; i < N2; i++ {
				diff := math.Abs(float64(libTrig[i]) - float64(goTrig[i]))

				if diff > maxDiff {
					maxDiff = diff
					maxIdx = i
				}

				// Check for exact bit match
				if libTrig[i] != goTrig[i] {
					mismatchCount++
					if mismatchCount <= 5 {
						t.Logf("  Mismatch at i=%d: lib=%.9e, go=%.9e, diff=%.2e",
							i, libTrig[i], goTrig[i], diff)
					}
				}
			}

			t.Logf("N=%d: maxDiff=%.2e at idx %d, mismatches=%d/%d",
				N, maxDiff, maxIdx, mismatchCount, N2)

			// For float32, we expect bit-exact match since both use same formula and PI constant
			if mismatchCount > 0 {
				t.Errorf("MDCT trig has %d mismatches out of %d values", mismatchCount, N2)
			}
		})
	}
}

// TestMDCTTrig_MathPiVsLibopusPi checks if using math.Pi vs LibopusPi causes differences.
func TestMDCTTrig_MathPiVsLibopusPi(t *testing.T) {
	N := 1920
	N2 := N / 2

	t.Logf("math.Pi       = %.20f", math.Pi)
	t.Logf("LibopusPi     = %.20f", LibopusPi)
	t.Logf("Difference    = %.2e", math.Abs(math.Pi-LibopusPi))

	// Compute with math.Pi
	trigMathPi := make([]float32, N2)
	for i := 0; i < N2; i++ {
		angle := 2.0 * math.Pi * (float64(i) + 0.125) / float64(N)
		trigMathPi[i] = float32(math.Cos(angle))
	}

	// Compute with LibopusPi
	trigLibPi := make([]float32, N2)
	for i := 0; i < N2; i++ {
		angle := 2.0 * LibopusPi * (float64(i) + 0.125) / float64(N)
		trigLibPi[i] = float32(math.Cos(angle))
	}

	// Compare
	var maxDiff float64
	var diffCount int
	for i := 0; i < N2; i++ {
		diff := math.Abs(float64(trigMathPi[i]) - float64(trigLibPi[i]))
		if diff > maxDiff {
			maxDiff = diff
		}
		if trigMathPi[i] != trigLibPi[i] {
			diffCount++
		}
	}

	t.Logf("N=%d: maxDiff=%.2e, different values=%d/%d", N, maxDiff, diffCount, N2)

	// This test is informational - it shows whether PI precision matters for float32
	if diffCount > 0 {
		t.Logf("NOTE: Using math.Pi vs LibopusPi produces %d different float32 values", diffCount)
	} else {
		t.Log("NOTE: Using math.Pi vs LibopusPi produces identical float32 values")
	}
}

// TestFFTTwiddles_VerifyFormula verifies the FFT twiddle formula is correct.
// exp(-j * 2*pi*i/N) = cos(-2*pi*i/N) + j*sin(-2*pi*i/N)
func TestFFTTwiddles_VerifyFormula(t *testing.T) {
	nfft := 480

	// Get libopus twiddles
	libR, libI := ComputeLibopusFFTTwiddles(nfft)

	// Verify properties
	// w[0] should be (1, 0)
	if math.Abs(float64(libR[0])-1.0) > 1e-6 || math.Abs(float64(libI[0])) > 1e-6 {
		t.Errorf("w[0] should be (1, 0), got (%.9e, %.9e)", libR[0], libI[0])
	}

	// w[N/4] should be (0, -1) for exp(-j*pi/2)
	quarter := nfft / 4
	if math.Abs(float64(libR[quarter])) > 1e-5 || math.Abs(float64(libI[quarter])+1.0) > 1e-5 {
		t.Errorf("w[N/4] should be (0, -1), got (%.9e, %.9e)", libR[quarter], libI[quarter])
	}

	// w[N/2] should be (-1, 0) for exp(-j*pi)
	half := nfft / 2
	if math.Abs(float64(libR[half])+1.0) > 1e-6 || math.Abs(float64(libI[half])) > 1e-5 {
		t.Errorf("w[N/2] should be (-1, 0), got (%.9e, %.9e)", libR[half], libI[half])
	}

	// All twiddles should have magnitude close to 1
	for i := 0; i < nfft; i++ {
		mag := math.Sqrt(float64(libR[i])*float64(libR[i]) + float64(libI[i])*float64(libI[i]))
		if math.Abs(mag-1.0) > 1e-6 {
			t.Errorf("w[%d] has magnitude %.9f, expected 1.0", i, mag)
		}
	}

	t.Log("FFT twiddle formula verification passed")
}

// TestMDCTTrig_VerifyFormula verifies the MDCT trig formula is correct.
// trig[i] = cos(2*PI*(i+0.125)/N) for i=0..N/2-1
func TestMDCTTrig_VerifyFormula(t *testing.T) {
	N := 1920
	N2 := N / 2

	// Get libopus trig
	libTrig := ComputeLibopusMDCTTrig(N)

	// Verify trig[0] = cos(2*PI*0.125/N) = cos(PI/4/N)
	expected0 := float32(math.Cos(2.0 * LibopusPi * 0.125 / float64(N)))
	if libTrig[0] != expected0 {
		t.Errorf("trig[0] = %.9e, expected %.9e", libTrig[0], expected0)
	}

	// All trig values should be in range [-1, 1]
	for i := 0; i < N2; i++ {
		if libTrig[i] < -1.0 || libTrig[i] > 1.0 {
			t.Errorf("trig[%d] = %.9e is out of range [-1, 1]", i, libTrig[i])
		}
	}

	// Verify the quarter-period point: cos(PI/2) should be close to 0
	// At i = N/4 - 0.125 = N/4 (approximately), angle = 2*PI*(N/4+0.125)/N = PI/2 + small
	quarterIdx := N / 4
	expectedQuarter := float32(math.Cos(2.0 * LibopusPi * (float64(quarterIdx) + 0.125) / float64(N)))
	if libTrig[quarterIdx] != expectedQuarter {
		t.Errorf("trig[%d] = %.9e, expected %.9e", quarterIdx, libTrig[quarterIdx], expectedQuarter)
	}

	t.Log("MDCT trig formula verification passed")
}

// Helper function to format test names consistently
func formatTestName(format string, args ...interface{}) string {
	return formatString(format, args...)
}

func formatString(format string, args ...interface{}) string {
	result := format
	for i, arg := range args {
		placeholder := "%d"
		if i == 0 {
			result = replaceFirst(result, placeholder, formatArg(arg))
		}
	}
	return result
}

func replaceFirst(s, old, new string) string {
	idx := 0
	for i := 0; i < len(s)-len(old)+1; i++ {
		if s[i:i+len(old)] == old {
			idx = i
			break
		}
	}
	return s[:idx] + new + s[idx+len(old):]
}

func formatArg(arg interface{}) string {
	switch v := arg.(type) {
	case int:
		return intToString(v)
	default:
		return "?"
	}
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	if negative {
		digits = append(digits, '-')
	}
	// Reverse
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

// TestMDCTTrig_GoImplementation tests that the actual Go getMDCTTrigF32 function
// produces values matching libopus.
func TestMDCTTrig_GoImplementation(t *testing.T) {
	// Test MDCT sizes
	mdctSizes := []int{1920, 960, 480, 240}

	for _, N := range mdctSizes {
		t.Run(formatTestName("N=%d", N), func(t *testing.T) {
			N2 := N / 2

			// Get libopus trig table
			libTrig := ComputeLibopusMDCTTrig(N)

			// Get Go implementation's trig table
			goTrig := getMDCTTrigF32Go(N)

			// Compare
			var maxDiff float64
			var maxIdx int
			var mismatchCount int

			for i := 0; i < N2; i++ {
				diff := math.Abs(float64(libTrig[i]) - float64(goTrig[i]))

				if diff > maxDiff {
					maxDiff = diff
					maxIdx = i
				}

				if libTrig[i] != goTrig[i] {
					mismatchCount++
					if mismatchCount <= 5 {
						t.Logf("  Mismatch at i=%d: lib=%.9e, go=%.9e, diff=%.2e",
							i, libTrig[i], goTrig[i], diff)
					}
				}
			}

			t.Logf("N=%d: maxDiff=%.2e at idx %d, mismatches=%d/%d",
				N, maxDiff, maxIdx, mismatchCount, N2)

			if mismatchCount > 0 {
				t.Errorf("Go MDCT trig implementation has %d mismatches out of %d values", mismatchCount, N2)
			}
		})
	}
}

// getMDCTTrigF32Go matches the celt package's getMDCTTrigF32 implementation.
// Duplicated here to avoid import cycle.
func getMDCTTrigF32Go(n int) []float32 {
	n2 := n / 2
	trig := make([]float32, n2)
	for i := 0; i < n2; i++ {
		angle := 2.0 * math.Pi * (float64(i) + 0.125) / float64(n)
		trig[i] = float32(math.Cos(angle))
	}
	return trig
}

// TestFFTTwiddles_GoImplementation tests that the actual Go computeTwiddles function
// produces values matching libopus.
func TestFFTTwiddles_GoImplementation(t *testing.T) {
	fftSizes := []int{480, 240, 120, 60}

	for _, nfft := range fftSizes {
		t.Run(formatTestName("nfft=%d", nfft), func(t *testing.T) {
			// Get libopus twiddles
			libR, libI := ComputeLibopusFFTTwiddles(nfft)

			// Get Go implementation's twiddles
			goTwiddles := computeTwiddlesGo(nfft)

			// Compare
			var maxDiffR, maxDiffI float64
			var maxIdxR, maxIdxI int
			var mismatchCount int

			for i := 0; i < nfft; i++ {
				diffR := math.Abs(float64(libR[i]) - float64(goTwiddles[i].r))
				diffI := math.Abs(float64(libI[i]) - float64(goTwiddles[i].i))

				if diffR > maxDiffR {
					maxDiffR = diffR
					maxIdxR = i
				}
				if diffI > maxDiffI {
					maxDiffI = diffI
					maxIdxI = i
				}

				if libR[i] != goTwiddles[i].r || libI[i] != goTwiddles[i].i {
					mismatchCount++
					if mismatchCount <= 5 {
						t.Logf("  Mismatch at i=%d: libR=%.9e, goR=%.9e, diffR=%.2e",
							i, libR[i], goTwiddles[i].r, diffR)
						t.Logf("                   : libI=%.9e, goI=%.9e, diffI=%.2e",
							libI[i], goTwiddles[i].i, diffI)
					}
				}
			}

			t.Logf("nfft=%d: maxDiffR=%.2e at idx %d, maxDiffI=%.2e at idx %d, mismatches=%d/%d",
				nfft, maxDiffR, maxIdxR, maxDiffI, maxIdxI, mismatchCount, nfft)

			if mismatchCount > 0 {
				t.Errorf("Go FFT twiddles implementation has %d mismatches out of %d values", mismatchCount, nfft)
			}
		})
	}
}

// kissCpxGo is a local copy of the kissCpx struct.
type kissCpxGo struct {
	r float32
	i float32
}

// computeTwiddlesGo matches the celt package's computeTwiddles implementation.
// Duplicated here to avoid import cycle.
func computeTwiddlesGo(nfft int) []kissCpxGo {
	w := make([]kissCpxGo, nfft)
	const pi = 3.14159265358979323846264338327
	for i := 0; i < nfft; i++ {
		phase := (-2.0 * pi / float64(nfft)) * float64(i)
		w[i].r = float32(math.Cos(phase))
		w[i].i = float32(math.Sin(phase))
	}
	return w
}

// TestMDCTTrig_AllFormatsConsistent verifies that all trig table functions
// in the codebase produce consistent results with libopus.
func TestMDCTTrig_AllFormatsConsistent(t *testing.T) {
	// Test different MDCT sizes
	mdctSizes := []int{1920, 960, 480, 240}

	for _, N := range mdctSizes {
		t.Run(formatTestName("N=%d", N), func(t *testing.T) {
			N2 := N / 2

			// Get libopus reference
			libTrig := ComputeLibopusMDCTTrig(N)

			// Test getMDCTTrigF32 (mdct.go)
			mdctTrigF32 := getMDCTTrigF32Go(N)

			// Test getLibopusTrigF32 (mdct_libopus.go)
			libopusTrigF32 := getLibopusTrigF32Go(N)

			// Compare both against libopus
			var mismatchMDCT, mismatchLibopus int
			for i := 0; i < N2; i++ {
				if libTrig[i] != mdctTrigF32[i] {
					mismatchMDCT++
				}
				if libTrig[i] != libopusTrigF32[i] {
					mismatchLibopus++
				}
			}

			t.Logf("N=%d: getMDCTTrigF32 mismatches=%d, getLibopusTrigF32 mismatches=%d",
				N, mismatchMDCT, mismatchLibopus)

			if mismatchMDCT > 0 {
				t.Errorf("getMDCTTrigF32 has %d mismatches vs libopus", mismatchMDCT)
			}
			if mismatchLibopus > 0 {
				t.Errorf("getLibopusTrigF32 has %d mismatches vs libopus", mismatchLibopus)
			}
		})
	}
}

// getLibopusTrigF32Go matches the celt package's getLibopusTrigF32 implementation.
// Duplicated here to avoid import cycle.
func getLibopusTrigF32Go(n int) []float32 {
	n2 := n / 2
	trig := make([]float32, n2)
	for i := 0; i < n2; i++ {
		// Compute in float64 then cast to float32, matching libopus initialization
		trig[i] = float32(math.Cos(2.0 * math.Pi * (float64(i) + 0.125) / float64(n)))
	}
	return trig
}
