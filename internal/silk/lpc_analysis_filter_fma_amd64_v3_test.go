//go:build amd64.v3

package silk

import "testing"

func TestLPCAnalysisFilterF32V3ScalarZeroAlloc(t *testing.T) {
	const length = 320
	s := make([]float32, length)
	for i := range s {
		s[i] = float32(i%17) - 8
	}
	coef := make([]float32, 16)
	for i := range coef {
		coef[i] = 0.01 * float32(i+1)
	}
	out := make([]float32, length)
	lpcAnalysisFilterF32Scalar(out, coef, s, length, len(coef))
	if allocs := testing.AllocsPerRun(100, func() {
		lpcAnalysisFilterF32Scalar(out, coef, s, length, len(coef))
	}); allocs != 0 {
		t.Fatalf("v3 scalar LPC analysis filter allocated %v times", allocs)
	}
}
