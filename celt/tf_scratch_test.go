package celt

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestTFAnalysisScratchUsesLibopusNormWidth(t *testing.T) {
	libopustest.RequireOracle(t)
	sizes, err := probeLibopusCELTTypeSizes()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	var scratch TFAnalysisScratch
	scratch.EnsureTFAnalysisScratch(MaxBands, 384)
	if got := unsafe.Sizeof(scratch.Tmp[0]); got != uintptr(sizes.celtNorm) {
		t.Fatalf("Tmp element size=%d want libopus celt_norm size %d", got, sizes.celtNorm)
	}
	if got := unsafe.Sizeof(scratch.Tmp1[0]); got != uintptr(sizes.celtNorm) {
		t.Fatalf("Tmp1 element size=%d want libopus celt_norm size %d", got, sizes.celtNorm)
	}
}

func TestTFAnalysisWithScratchMatchesAllocatingPath(t *testing.T) {
	tests := []struct {
		name        string
		lm          int
		transient   bool
		tfEstimate  float64
		effective   int
		importance  []int32
		coefficient func(int) float64
	}{
		{
			name:       "nontransient_lm3",
			lm:         3,
			tfEstimate: 0.42,
			effective:  180,
			coefficient: func(i int) float64 {
				return float64((i%17)-8) / 9.0
			},
		},
		{
			name:       "transient_lm2_importance",
			lm:         2,
			transient:  true,
			tfEstimate: 0.17,
			effective:  96,
			importance: []int32{13, 14, 12, 15, 13, 11, 16, 13, 12, 15, 14, 13, 11, 12, 13, 14, 15, 13, 12, 11, 13},
			coefficient: func(i int) float64 {
				if i%37 == 0 {
					return 2.75
				}
				return float64((i%23)-11) / 13.0
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nbBands := MaxBands
			n0 := EBands[nbBands] << tc.lm
			x := make([]float64, n0)
			for i := range x {
				x[i] = tc.coefficient(i)
			}
			xNorm := float64sToNorms(x)
			wantRes, wantSelect := TFAnalysis(xNorm, n0, nbBands, tc.transient, tc.lm, opusVal16(tc.tfEstimate), tc.effective, tc.importance)
			var scratch TFAnalysisScratch
			gotRes, gotSelect := TFAnalysisWithScratch(xNorm, n0, nbBands, tc.transient, tc.lm, opusVal16(tc.tfEstimate), tc.effective, tc.importance, &scratch)
			if gotSelect != wantSelect || !reflect.DeepEqual(gotRes, wantRes) {
				t.Fatalf("scratch TF = select %d res %v, want select %d res %v", gotSelect, gotRes, wantSelect, wantRes)
			}
		})
	}
}
