package rdovae

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestRDOVAESGEMVFusedMatchesLibopusNEONOracle(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("NEON sgemv path is arm64-only")
	}
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
			sgemvFused(got, view, tc.rows, tc.cols, colStride, x)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("out[%d]=%s want %s", i, formatDNNKernelFloat(got[i]), formatDNNKernelFloat(want[i]))
				}
			}
		})
	}
}

func TestRDOVAECGEMV8x4MatchesLibopusNEONOracle(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("NEON cgemv8x4 path is arm64-only")
	}
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

func formatDNNKernelFloat(v float32) string {
	return fmt.Sprintf("0x%08x(%0.10g)", math.Float32bits(v), v)
}
