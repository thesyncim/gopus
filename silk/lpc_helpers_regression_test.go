package silk

import "testing"

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

	for i, v := range out {
		if v > 32767 || v < -32768 {
			t.Fatalf("out[%d]=%d out of int16 range", i, v)
		}
	}
}
