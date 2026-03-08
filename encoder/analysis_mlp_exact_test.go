package encoder

import (
	"math/rand"
	"reflect"
	"testing"
)

func gemmAccumF32GenericReference(out []float32, weights []float32, rows, cols, colStride int, x []float32) {
	if rows <= 0 || cols <= 0 {
		return
	}
	for j := 0; j < cols; j++ {
		xj := x[j]
		w := weights[j*colStride : j*colStride+rows]
		for i := 0; i < rows; i++ {
			out[i] += w[i] * xj
		}
	}
}

func TestGemmAccumF32MatchesGenericReference(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	for _, tc := range []struct {
		rows      int
		cols      int
		colStride int
	}{
		{rows: 2, cols: 24, colStride: 2},
		{rows: 24, cols: 32, colStride: 72},
		{rows: 32, cols: 25, colStride: 32},
		{rows: 7, cols: 9, colStride: 11},
	} {
		outCurrent := make([]float32, tc.rows)
		outWant := make([]float32, tc.rows)
		weights := make([]float32, tc.cols*tc.colStride)
		input := make([]float32, tc.cols)
		for i := range outCurrent {
			v := rng.Float32()*2 - 1
			outCurrent[i] = v
			outWant[i] = v
		}
		for i := range weights {
			weights[i] = rng.Float32()*2 - 1
		}
		for i := range input {
			input[i] = rng.Float32()*2 - 1
		}

		gemmAccumF32(outCurrent, weights, tc.rows, tc.cols, tc.colStride, input)
		gemmAccumF32GenericReference(outWant, weights, tc.rows, tc.cols, tc.colStride, input)

		if !reflect.DeepEqual(outCurrent, outWant) {
			t.Fatalf("rows=%d cols=%d stride=%d mismatch", tc.rows, tc.cols, tc.colStride)
		}
	}
}
