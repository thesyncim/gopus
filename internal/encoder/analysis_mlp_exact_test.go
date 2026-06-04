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

func TestGemmAccumF32Rows24PairMatchesSeparatePasses(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	rows := 24
	cols := layer1.NbNeurons
	stride := 3 * rows
	out0 := make([]float32, rows)
	out1 := make([]float32, rows)
	want0 := make([]float32, rows)
	want1 := make([]float32, rows)
	input := make([]float32, cols)
	for i := range out0 {
		v0 := rng.Float32()*2 - 1
		v1 := rng.Float32()*2 - 1
		out0[i], want0[i] = v0, v0
		out1[i], want1[i] = v1, v1
	}
	for i := range input {
		input[i] = rng.Float32()*2 - 1
	}

	gemmAccumF32Rows24Pair(out0, out1, layer1.recurrentWeightsF32, cols, stride, input)
	gemmAccumF32(want0, layer1.recurrentWeightsF32, rows, cols, stride, input)
	gemmAccumF32(want1, layer1.recurrentWeightsF32[rows:], rows, cols, stride, input)

	if !reflect.DeepEqual(out0, want0) || !reflect.DeepEqual(out1, want1) {
		t.Fatal("rows24 pair mismatch")
	}
}

func TestGemmAccumF32Rows24TripleMatchesSeparatePasses(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	rows := 24
	cols := layer1.NbInputs
	stride := 3 * rows
	out0 := make([]float32, rows)
	out1 := make([]float32, rows)
	out2 := make([]float32, rows)
	want0 := make([]float32, rows)
	want1 := make([]float32, rows)
	want2 := make([]float32, rows)
	input := make([]float32, cols)
	for i := range out0 {
		v0 := rng.Float32()*2 - 1
		v1 := rng.Float32()*2 - 1
		v2 := rng.Float32()*2 - 1
		out0[i], want0[i] = v0, v0
		out1[i], want1[i] = v1, v1
		out2[i], want2[i] = v2, v2
	}
	for i := range input {
		input[i] = rng.Float32()*2 - 1
	}

	gemmAccumF32Rows24Triple(out0, out1, out2, layer1.inputWeightsF32, cols, stride, input)
	gemmAccumF32(want0, layer1.inputWeightsF32, rows, cols, stride, input)
	gemmAccumF32(want1, layer1.inputWeightsF32[rows:], rows, cols, stride, input)
	gemmAccumF32(want2, layer1.inputWeightsF32[2*rows:], rows, cols, stride, input)

	if !reflect.DeepEqual(out0, want0) || !reflect.DeepEqual(out1, want1) || !reflect.DeepEqual(out2, want2) {
		t.Fatal("rows24 triple mismatch")
	}
}
