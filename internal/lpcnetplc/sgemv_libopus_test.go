package lpcnetplc

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusDNNKernelInputMagic  = "GDKI"
	libopusDNNKernelOutputMagic = "GDKO"

	libopusDNNKernelSGEMV    = uint32(0)
	libopusDNNKernelCGEMV8x4 = uint32(1)
)

var libopusDNNKernelHelper libopustest.HelperCache

func getLibopusDNNKernelHelperPath() (string, error) {
	return libopusDNNKernelHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "dnn kernel",
		OutputBase:  "gopus_libopus_dnn_kernel",
		SourceFile:  "libopus_dnn_kernel_info.c",
		RefIncludes: []string{"celt", "celt/x86", "dnn"},
		Libs:        []string{"-lm"},
	})
}

func probeLibopusDNNSGEMV(rows, cols, colStride int, weights, x []float32) ([]float32, error) {
	binPath, err := getLibopusDNNKernelHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusDNNKernelInputMagic, libopusDNNKernelSGEMV, uint32(rows), uint32(cols), uint32(colStride))
	payload.Float32s(weights...)
	payload.Float32s(x...)
	return readLibopusDNNKernelOracle(binPath, payload.Bytes(), rows)
}

func probeLibopusDNNCGEMV8x4(rows, cols int, weights []byte, scale, x []float32) ([]float32, error) {
	binPath, err := getLibopusDNNKernelHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusDNNKernelInputMagic, libopusDNNKernelCGEMV8x4, uint32(rows), uint32(cols), 0)
	payload.Raw(weights)
	payload.Float32s(scale...)
	payload.Float32s(x...)
	return readLibopusDNNKernelOracle(binPath, payload.Bytes(), rows)
}

func readLibopusDNNKernelOracle(binPath string, input []byte, rows int) ([]float32, error) {
	reader, err := libopustest.RunOracle(binPath, input, "dnn kernel", libopusDNNKernelOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(rows)
	reader.ExpectRemaining(4 * count)
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSGEMVFusedMatchesLibopusNEONOracle(t *testing.T) {
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
			weights := deterministicSGEMVFloats(tc.cols * colStride)
			x := deterministicSGEMVFloats(tc.cols)
			want, err := probeLibopusDNNSGEMV(tc.rows, tc.cols, colStride, weights, x)
			if err != nil {
				libopustest.HelperUnavailable(t, "dnn sgemv", err)
			}
			view, err := dnnblob.Float32ViewFromBytes(float32Bytes(weights), int32(4*len(weights)))
			if err != nil {
				t.Fatalf("Float32ViewFromBytes error: %v", err)
			}
			got := make([]float32, tc.rows)
			sgemvFused(got, view, tc.rows, tc.cols, colStride, x)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("out[%d]=%s want %s", i, formatSGEMVFloat(got[i]), formatSGEMVFloat(want[i]))
				}
			}
		})
	}
}

func TestCGEMV8x4MatchesLibopusNEONOracle(t *testing.T) {
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
			weights := deterministicInt8Weights(tc.rows * tc.cols)
			scale := deterministicSGEMVFloats(tc.rows)
			x := deterministicSGEMVFloats(tc.cols)
			want, err := probeLibopusDNNCGEMV8x4(tc.rows, tc.cols, weights, scale, x)
			if err != nil {
				libopustest.HelperUnavailable(t, "dnn cgemv8x4", err)
			}
			weightView, err := dnnblob.Int8ViewFromBytes(weights, int32(len(weights)))
			if err != nil {
				t.Fatalf("Int8ViewFromBytes error: %v", err)
			}
			scaleView, err := dnnblob.Float32ViewFromBytes(float32Bytes(scale), int32(4*len(scale)))
			if err != nil {
				t.Fatalf("Float32ViewFromBytes(scale) error: %v", err)
			}
			got := make([]float32, tc.rows)
			quant := make([]int16, tc.cols)
			cgemv8x4(got, weightView, scaleView, tc.rows, tc.cols, x, quant)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("out[%d]=%s want %s", i, formatSGEMVFloat(got[i]), formatSGEMVFloat(want[i]))
				}
			}
		})
	}
}

func deterministicSGEMVFloats(n int) []float32 {
	out := make([]float32, n)
	seed := uint32(0x9e3779b9)
	for i := range out {
		seed = 1664525*seed + 1013904223
		out[i] = float32(int32(seed%2001)-1000) * (1.0 / 4096.0)
	}
	return out
}

func deterministicInt8Weights(n int) []byte {
	out := make([]byte, n)
	seed := uint32(0x243f6a88)
	for i := range out {
		seed = 1103515245*seed + 12345
		out[i] = byte(int8(int32(seed%255) - 127))
	}
	return out
}

func float32Bytes(values []float32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[4*i:4*i+4], math.Float32bits(v))
	}
	return out
}

func formatSGEMVFloat(v float32) string {
	return fmt.Sprintf("0x%08x(%0.10g)", math.Float32bits(v), v)
}
