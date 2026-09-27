package lpcnetplc

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// The selected DRED archive's compute_linear and compute_conv2d are the
// kernels libopus's PLC, FARGAN and PitchDNN models run through, so these
// checks pair the Go layer paths with the instruction lane under test.

func TestLinearFloatMatchesSelectedLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		rows int
		cols int
	}{
		{rows: 1, cols: 9},
		{rows: 16, cols: 9},
		{rows: 31, cols: 6},
	} {
		t.Run(fmt.Sprintf("rows_%d_cols_%d", tc.rows, tc.cols), func(t *testing.T) {
			weights := roundingDNNFloats(tc.rows * tc.cols)
			x := roundingDNNFloats(tc.cols)
			want, err := libopustest.ProbeDNNKernelSGEMV(tc.rows, tc.cols, tc.rows, weights, x)
			if err != nil {
				libopustest.HelperUnavailable(t, "selected dnn sgemv", err)
			}
			view, err := dnnblob.Float32ViewFromBytes(float32Bytes(weights), int32(4*len(weights)))
			if err != nil {
				t.Fatal(err)
			}
			layer := LinearLayer{FloatWeights: view, NbInputs: tc.cols, NbOutputs: tc.rows}
			var scratch predictorScratch
			got := make([]float32, tc.rows)
			computeLinear(&layer, got, x, &scratch)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("out[%d]=%s want %s", i, formatSGEMVFloat(got[i]), formatSGEMVFloat(want[i]))
				}
			}
			if allocs := testing.AllocsPerRun(100, func() { computeLinear(&layer, got, x, &scratch) }); allocs != 0 {
				t.Fatalf("warm float linear allocs=%g want 0", allocs)
			}
		})
	}
}

func TestLinearIntegerMatchesSelectedLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name  string
		rows  int
		cols  int
		scale float32
	}{
		{name: "unit_8x8", rows: 8, cols: 8, scale: 1},
		{name: "unit_16x24", rows: 16, cols: 24, scale: 1},
		// PLC and FARGAN feed unbounded features into integer layers, so
		// cover inputs beyond the [-1, 1] quantizer range.
		{name: "wide_16x16", rows: 16, cols: 16, scale: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			weights := deterministicInt8Weights(tc.rows * tc.cols)
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
			x := roundingDNNFloats(tc.cols)
			for i := range x {
				x[i] *= 4 * tc.scale
			}
			x[0] = tc.scale
			want, err := libopustest.ProbeDNNLinearCGEMV8x4(tc.rows, tc.cols, nil, weights, scale, x, bias, subias)
			if err != nil {
				libopustest.HelperUnavailable(t, "selected dnn integer linear", err)
			}
			layer := LinearLayer{
				Weights:   mustInt8View(t, weights),
				Scale:     mustFloat32View(t, scale),
				Bias:      mustFloat32View(t, bias),
				Subias:    mustFloat32View(t, subias),
				NbInputs:  tc.cols,
				NbOutputs: tc.rows,
			}
			var scratch predictorScratch
			got := make([]float32, tc.rows)
			computeLinear(&layer, got, x, &scratch)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("out[%d]=%s want %s", i, formatSGEMVFloat(got[i]), formatSGEMVFloat(want[i]))
				}
			}
			if allocs := testing.AllocsPerRun(100, func() { computeLinear(&layer, got, x, &scratch) }); allocs != 0 {
				t.Fatalf("warm integer linear allocs=%g want 0", allocs)
			}
		})
	}
}

func TestConv2DMatchesSelectedLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		outChannels int
		inChannels  int
		height      int
	}{
		{outChannels: 8, inChannels: 1, height: 16},
		{outChannels: 3, inChannels: 4, height: 21},
	} {
		t.Run(fmt.Sprintf("out_%d_in_%d_h_%d", tc.outChannels, tc.inChannels, tc.height), func(t *testing.T) {
			timeStride := tc.inChannels * (tc.height + 2)
			weights := roundingDNNFloats(tc.outChannels * tc.inChannels * 9)
			bias := roundingDNNFloats(tc.outChannels + 3)[3:]
			stream := roundingDNNFloats(3*timeStride + 5)[5:]
			for i := range stream {
				stream[i] *= 8
			}
			mem := stream[:2*timeStride]
			in := stream[2*timeStride:]
			want, err := libopustest.ProbeDNNConv2D3x3(tc.outChannels, tc.inChannels, tc.height, weights, bias, mem, in)
			if err != nil {
				libopustest.HelperUnavailable(t, "selected dnn conv2d", err)
			}
			layer := Conv2DLayer{
				Bias:         mustFloat32View(t, bias),
				FloatWeights: mustFloat32View(t, weights),
				InChannels:   tc.inChannels,
				OutChannels:  tc.outChannels,
				KTime:        3,
				KHeight:      3,
			}
			var scratch pitchDNNScratch
			goMem := append([]float32(nil), mem...)
			got := make([]float32, tc.outChannels*tc.height)
			computeConv2D(&layer, got, goMem, in, tc.height, tc.height, activationTanh, &scratch)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("out[%d]=%s want %s", i, formatSGEMVFloat(got[i]), formatSGEMVFloat(want[i]))
				}
			}
			if allocs := testing.AllocsPerRun(100, func() {
				computeConv2D(&layer, got, goMem, in, tc.height, tc.height, activationTanh, &scratch)
			}); allocs != 0 {
				t.Fatalf("warm conv2d allocs=%g want 0", allocs)
			}
		})
	}
}

func mustFloat32View(t *testing.T, values []float32) dnnblob.Float32View {
	t.Helper()
	view, err := dnnblob.Float32ViewFromBytes(float32Bytes(values), int32(4*len(values)))
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func mustInt8View(t *testing.T, values []byte) dnnblob.Int8View {
	t.Helper()
	view, err := dnnblob.Int8ViewFromBytes(values, int32(len(values)))
	if err != nil {
		t.Fatal(err)
	}
	return view
}

// roundingDNNFloats returns values in (-1, 1) with full 24-bit mantissas, so
// products round and fused versus separate multiply-adds differ.
func roundingDNNFloats(n int) []float32 {
	out := make([]float32, n)
	seed := uint32(0x6a09e667)
	for i := range out {
		seed = 1664525*seed + 1013904223
		out[i] = float32(int32(seed>>7)-(1<<24)) * (1.0 / (1 << 24))
	}
	return out
}
