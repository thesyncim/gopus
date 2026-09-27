package rdovae

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestRDOVAESGEMVMatchesSelectedLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, tc := range []struct {
		rows int
		cols int
	}{
		{rows: 16, cols: 9},
		{rows: 8, cols: 13},
		{rows: 5, cols: 7},
	} {
		t.Run(fmt.Sprintf("rows_%d_cols_%d", tc.rows, tc.cols), func(t *testing.T) {
			colStride := tc.rows
			weights := deterministicDNNFloats(tc.cols * colStride)
			x := deterministicDNNFloats(tc.cols)
			want, err := libopustest.ProbeDNNKernelSGEMV(tc.rows, tc.cols, colStride, weights, x)
			if err != nil {
				libopustest.HelperUnavailable(t, "dnn sgemv", err)
			}
			view, err := dnnblob.Float32ViewFromBytes(float32DNNBytes(weights), int32(4*len(weights)))
			if err != nil {
				t.Fatalf("Float32ViewFromBytes error: %v", err)
			}
			got := make([]float32, tc.rows)
			sgemv(got, view, tc.rows, tc.cols, colStride, x)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("out[%d]=%s want %s", i, formatDNNKernelFloat(got[i]), formatDNNKernelFloat(want[i]))
				}
			}
			if allocs := testing.AllocsPerRun(100, func() { sgemv(got, view, tc.rows, tc.cols, colStride, x) }); allocs != 0 {
				t.Fatalf("warm SGEMV allocs=%g want 0", allocs)
			}
		})
	}
}

func TestRDOVAECGEMV8x4MatchesSelectedLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, tc := range []struct {
		rows int
		cols int
	}{
		{rows: 8, cols: 8},
		{rows: 16, cols: 16},
	} {
		t.Run(fmt.Sprintf("rows_%d_cols_%d", tc.rows, tc.cols), func(t *testing.T) {
			weights := deterministicDNNInt8Weights(tc.rows * tc.cols)
			scale := deterministicDNNFloats(tc.rows)
			x := deterministicDNNFloats(tc.cols)
			want, err := libopustest.ProbeDNNKernelCGEMV8x4(tc.rows, tc.cols, weights, scale, x)
			if err != nil {
				libopustest.HelperUnavailable(t, "dnn cgemv8x4", err)
			}
			weightView, err := dnnblob.Int8ViewFromBytes(weights, int32(len(weights)))
			if err != nil {
				t.Fatalf("Int8ViewFromBytes error: %v", err)
			}
			scaleView, err := dnnblob.Float32ViewFromBytes(float32DNNBytes(scale), int32(4*len(scale)))
			if err != nil {
				t.Fatalf("Float32ViewFromBytes(scale) error: %v", err)
			}
			got := make([]float32, tc.rows)
			quant := make([]int8, tc.cols)
			cgemv8x4(got, weightView, scaleView, tc.rows, tc.cols, x, quant)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("out[%d]=%s want %s", i, formatDNNKernelFloat(got[i]), formatDNNKernelFloat(want[i]))
				}
			}
		})
	}
}

func TestRDOVAEIntegerLinearBiasMatchesSelectedLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name    string
		rows    int
		cols    int
		indices []int32
	}{
		{name: "dense_8x8", rows: 8, cols: 8},
		{name: "dense_16x16", rows: 16, cols: 16},
		{name: "sparse_8x8", rows: 8, cols: 8, indices: []int32{2, 0, 4}},
		{name: "sparse_16x16", rows: 16, cols: 16, indices: []int32{2, 0, 8, 1, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			weightCount := tc.rows * tc.cols
			if len(tc.indices) != 0 {
				weightCount = (len(tc.indices) - tc.rows/8) * 32
			}
			weights := deterministicDNNInt8Weights(weightCount)
			for i := 0; i < 8 && i < len(weights); i++ {
				weights[i] = byte(int8(127))
			}
			scale := make([]float32, tc.rows)
			bias := make([]float32, tc.rows)
			subias := make([]float32, tc.rows)
			for i := range scale {
				scale[i] = float32(i+1) * (1.0 / 8192.0)
				bias[i] = float32(i-4) * (1.0 / 16.0)
				subias[i] = float32(i+7) * (1.0 / 32.0)
			}
			x := make([]float32, tc.cols)
			for i := range x {
				x[i] = 1
			}
			want, err := libopustest.ProbeDNNLinearCGEMV8x4(tc.rows, tc.cols, tc.indices, weights, scale, x, bias, subias)
			if err != nil {
				libopustest.HelperUnavailable(t, "selected DRED integer linear", err)
			}
			weightView, err := dnnblob.Int8ViewFromBytes(weights, int32(len(weights)))
			if err != nil {
				t.Fatal(err)
			}
			scaleView, err := dnnblob.Float32ViewFromBytes(float32DNNBytes(scale), int32(4*len(scale)))
			if err != nil {
				t.Fatal(err)
			}
			biasView, err := dnnblob.Float32ViewFromBytes(float32DNNBytes(bias), int32(4*len(bias)))
			if err != nil {
				t.Fatal(err)
			}
			subiasView, err := dnnblob.Float32ViewFromBytes(float32DNNBytes(subias), int32(4*len(subias)))
			if err != nil {
				t.Fatal(err)
			}
			layer := LinearLayer{
				Weights: weightView, Scale: scaleView, Bias: biasView, Subias: subiasView,
				NbInputs: tc.cols, NbOutputs: tc.rows,
			}
			if len(tc.indices) != 0 {
				layer.WeightsIdx, err = dnnblob.Int32ViewFromBytes(int32DNNBytes(tc.indices), int32(4*len(tc.indices)))
				if err != nil {
					t.Fatal(err)
				}
			}
			got := make([]float32, tc.rows)
			var scratch runtimeScratch
			computeLinear(&layer, got, x, &scratch)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("out[%d]=%s want %s", i, formatDNNKernelFloat(got[i]), formatDNNKernelFloat(want[i]))
				}
			}
			if allocs := testing.AllocsPerRun(100, func() { computeLinear(&layer, got, x, &scratch) }); allocs != 0 {
				t.Fatalf("warm integer linear allocs=%g want 0", allocs)
			}
		})
	}
}

func TestRDOVAEIntegerInputQuantizerMatchesSelectedLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	weights := make([]byte, 8*8)
	weights[0] = 1
	scale := make([]float32, 8)
	scale[0] = 1
	zero := make([]float32, 8)
	weightView, err := dnnblob.Int8ViewFromBytes(weights, int32(len(weights)))
	if err != nil {
		t.Fatal(err)
	}
	scaleView, err := dnnblob.Float32ViewFromBytes(float32DNNBytes(scale), int32(4*len(scale)))
	if err != nil {
		t.Fatal(err)
	}
	layer := LinearLayer{Weights: weightView, Scale: scaleView, NbInputs: 8, NbOutputs: 8}
	var scratch runtimeScratch
	got := make([]float32, 8)
	values := []float32{
		-1, math.Nextafter32(-0.5, -1), -0.5, math.Nextafter32(-0.5, 0),
		float32(-2.5 / 127), math.Nextafter32(float32(-2.5/127), 0),
		0, math.Nextafter32(float32(2.5/127), 0), float32(2.5 / 127),
		math.Nextafter32(0.5, 0), 0.5, math.Nextafter32(0.5, 1), 1,
	}
	if useX86DNNVectorKernels {
		values = append(values, 257, 258, math.Float32frombits(0x7fc01234), float32(math.Inf(1)), float32(math.Inf(-1)))
	}
	for _, value := range values {
		t.Run(fmt.Sprintf("bits_%08x", math.Float32bits(value)), func(t *testing.T) {
			x := make([]float32, 8)
			x[0] = value
			want, err := libopustest.ProbeDNNLinearCGEMV8x4(8, 8, nil, weights, scale, x, zero, zero)
			if err != nil {
				libopustest.HelperUnavailable(t, "selected DRED input quantizer", err)
			}
			computeLinear(&layer, got, x, &scratch)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("out[%d]=%s want %s", i, formatDNNKernelFloat(got[i]), formatDNNKernelFloat(want[i]))
				}
			}
		})
	}
}

func deterministicDNNFloats(n int) []float32 {
	out := make([]float32, n)
	seed := uint32(0x9e3779b9)
	for i := range out {
		seed = 1664525*seed + 1013904223
		out[i] = float32(int32(seed%2001)-1000) * (1.0 / 4096.0)
	}
	return out
}

func deterministicDNNInt8Weights(n int) []byte {
	out := make([]byte, n)
	seed := uint32(0x243f6a88)
	for i := range out {
		seed = 1103515245*seed + 12345
		out[i] = byte(int8(int32(seed%255) - 127))
	}
	return out
}

func float32DNNBytes(values []float32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[4*i:4*i+4], math.Float32bits(v))
	}
	return out
}

func int32DNNBytes(values []int32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[4*i:4*i+4], uint32(v))
	}
	return out
}

func formatDNNKernelFloat(v float32) string {
	return fmt.Sprintf("0x%08x(%0.10g)", math.Float32bits(v), v)
}
