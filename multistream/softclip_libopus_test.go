package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFloat64ToInt16SoftClipMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name     string
		n        int
		channels int
		mem      []float32
		samples  []float64
	}{
		{
			name:     "six channel clipped segments",
			n:        5,
			channels: 6,
			mem:      []float32{0, -0.04, 0.06, 0, 0.08, -0.03},
			samples: []float64{
				1.35, -1.6, 0.2, 0.95, -1.25, 1.9,
				1.7, -1.2, -0.3, 1.2, -1.55, 1.5,
				0.8, -0.4, -1.4, 1.6, -0.6, 0.1,
				-1.3, 1.4, -1.8, 0.4, 1.2, -1.1,
				0.2, 0.1, -0.5, -1.7, 0.8, -0.2,
			},
		},
		{
			name:     "three channel carryover",
			n:        8,
			channels: 3,
			mem:      []float32{-0.12, 0.09, -0.02},
			samples: []float64{
				0.7, -0.7, 0.5,
				0.4, -0.4, 0.25,
				-0.2, 0.2, -0.1,
				1.3, -1.4, 1.6,
				1.7, -1.2, 1.3,
				0.6, -0.5, -0.6,
				-1.5, 1.8, -1.9,
				0.3, -0.2, 0.1,
			},
		},
		{
			name:     "hard clamp before quant",
			n:        4,
			channels: 2,
			mem:      []float32{0, 0},
			samples:  []float64{2.5, -2.5, 3, -3, 1.2, -1.2, 0, 0.5},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oracleSamples := float64ToFloat32(tc.samples)
			wantFloat, wantMem, err := libopustest.ProbeSoftClip(tc.n, tc.channels, oracleSamples, tc.mem)
			if err != nil {
				libopustest.HelperUnavailable(t, "softclip", err)
			}
			want, err := libopustest.ProbeFloatQuant(libopustest.FloatQuantModeCELTDispatch, wantFloat)
			if err != nil {
				libopustest.HelperUnavailable(t, "float quant", err)
			}

			gotMem := append([]float32(nil), tc.mem...)
			got := float64ToInt16SoftClip(tc.samples, tc.n, tc.channels, gotMem)
			if len(got) < len(want) {
				t.Fatalf("output len=%d want at least %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%d want %d", i, got[i], want[i])
				}
			}
			for i := range wantMem {
				if math.Float32bits(gotMem[i]) != math.Float32bits(wantMem[i]) {
					t.Fatalf("mem[%d]=%g want %g", i, gotMem[i], wantMem[i])
				}
			}
		})
	}
}
