package celt

import (
	"fmt"
	"math"
	"math/cmplx"
	"testing"
)

func TestDFTVerify(t *testing.T) {
	// Test DFT with a simple impulse
	n := 8
	x := make([]complex128, n)
	x[0] = complex(1, 0) // DC impulse

	result := dft(x)
	fmt.Printf("DFT of impulse [1,0,0,0,0,0,0,0]:\n")
	for i, v := range result {
		fmt.Printf("  [%d] = (%.6f, %.6f)\n", i, real(v), imag(v))
	}
	fmt.Printf("\nExpected: all (1, 0)\n")

	// Test with sinusoid
	fmt.Printf("\n\nDFT of cosine at bin 1:\n")
	for i := 0; i < n; i++ {
		x[i] = complex(math.Cos(2*math.Pi*float64(i)/float64(n)), 0)
	}
	result = dft(x)
	for i, v := range result {
		fmt.Printf("  [%d] = (%.4f, %.4f) |%f|\n", i, real(v), imag(v), cmplx.Abs(v))
	}
	fmt.Printf("\nExpected: magnitude peak at bin 1 and bin 7 (n-1)\n")

	// Test larger DFT
	n = 480
	x = make([]complex128, n)
	x[0] = complex(1, -0.0003) // Similar to our IMDCT pre-rotate output

	result = dft(x)
	fmt.Printf("\n\nDFT of impulse (size %d):\n", n)
	fmt.Printf("  First 5:\n")
	for i := 0; i < 5; i++ {
		fmt.Printf("    [%d] = (%.6f, %.6f)\n", i, real(result[i]), imag(result[i]))
	}
	fmt.Printf("  Last 5:\n")
	for i := n - 5; i < n; i++ {
		fmt.Printf("    [%d] = (%.6f, %.6f)\n", i, real(result[i]), imag(result[i]))
	}
	fmt.Printf("\nExpected: all (1, -0.0003) approximately\n")

	// Check if all values are the same (as they should be for impulse)
	var maxDiff float64
	for i := 1; i < n; i++ {
		diff := cmplx.Abs(result[i] - result[0])
		if diff > maxDiff {
			maxDiff = diff
		}
	}
	fmt.Printf("Max difference from result[0]: %.10f\n", maxDiff)
}
