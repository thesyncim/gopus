//go:build amd64 && goexperiment.simd && !nosimd

package dnnmath

import (
	"encoding/binary"
	"math"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestX86SGEMVScalarRemainderMatchesSelectedLibopus(t *testing.T) {
	if !X86VectorKernels {
		t.Skip("selected AVX2/FMA DNN kernel requires AVX2 and FMA")
	}

	// The scalar row remainder in dnn/vec_avx.h is compiled in the AVX2/FMA
	// translation unit. This product pair distinguishes a fused update from
	// its independently rounded multiply and add:
	//   (1 + 2^-23) * (1 - 2^-23) - 1 == -2^-46 when fused, 0 when split.
	a := math.Float32frombits(0x3f800001)
	b := math.Float32frombits(0x3f7ffffe)
	for _, rows := range []int{1, 2, 3, 5, 17, 18, 19} {
		for _, cols := range []int{2, 3, 5, 17} {
			t.Run(testNameRowsCols(rows, cols), func(t *testing.T) {
				x := make([]float32, cols)
				x[0], x[1] = 1, b
				weights := make([]float32, rows*cols)
				for row := range rows {
					weights[row] = -1
					weights[rows+row] = a
				}
				want, err := libopustest.ProbeDNNKernelSGEMV(rows, cols, rows, weights, x)
				if err != nil {
					libopustest.HelperUnavailable(t, "selected AVX2 DNN SGEMV", err)
				}

				weightBytes := make([]byte, 4*len(weights))
				for i, weight := range weights {
					binary.LittleEndian.PutUint32(weightBytes[4*i:], math.Float32bits(weight))
				}
				view, err := dnnblob.Float32ViewFromBytes(weightBytes, int32(len(weightBytes)))
				if err != nil {
					t.Fatal(err)
				}
				got := make([]float32, rows)
				SGEMVX86(got, view, rows, cols, rows, x)
				for i := range got {
					if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
						t.Fatalf("rows=%d cols=%d out[%d]=%08x want selected C %08x", rows, cols, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
					}
				}
			})
		}
	}
}

func testNameRowsCols(rows, cols int) string {
	return "rows_" + strconv.Itoa(rows) + "_cols_" + strconv.Itoa(cols)
}
