//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestQEXTMDCTBackwardMatchesSelectedLibopus(t *testing.T) {
	cases := []struct {
		mode, shift, stride int
	}{
		{0, 0, 1}, {0, 1, 2}, {0, 2, 1}, {0, 3, 3},
		{1, 0, 1}, {1, 1, 2}, {1, 2, 1}, {1, 3, 3},
	}
	for _, tc := range cases {
		t.Run(qextMDCTCaseName(tc.mode, tc.shift, tc.stride), func(t *testing.T) {
			lookup, overlap, n := qextMDCTMode(tc.mode)
			frameN := n >> tc.shift
			input := make([]int32, tc.stride*(frameN/2-1)+1)
			rng := rand.New(rand.NewSource(int64(0x51d8 + tc.mode*100 + tc.shift*10 + tc.stride)))
			for i := range input {
				// celt_synthesis hands the inverse transform bounded Q12 signal
				// coefficients; this range also exercises the source's shift logic.
				input[i] = int32(rng.Intn(1<<27) - (1 << 26))
			}
			initial := make([]int32, frameN/2+overlap/2)
			for i := range initial {
				initial[i] = int32(rng.Intn(1<<23) - (1 << 22))
			}
			want, err := libopustest.ProbeCELTFixedQEXTMDCTBackward(libopustest.CELTFixedQEXTMDCTBackwardParams{
				Mode: tc.mode, Shift: tc.shift, Stride: tc.stride, Input: input, Initial: initial,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT inverse MDCT", err)
			}

			out := append([]int32(nil), initial...)
			scratch := &QEXTMDCTScratch{}
			lookup.MDCTBackward(input, out, nil, overlap, tc.shift, tc.stride, scratch)
			for i, got := range out {
				if got != want[i] {
					t.Fatalf("inverse output[%d]=%d, libopus=%d", i, got, want[i])
				}
			}

			allocs := testing.AllocsPerRun(10, func() {
				copy(out, initial)
				lookup.MDCTBackward(input, out, nil, overlap, tc.shift, tc.stride, scratch)
			})
			if allocs != 0 {
				t.Fatalf("steady-state allocations=%g, want 0", allocs)
			}
		})
	}
}

func qextMDCTMode(mode int) (*QEXTMDCTLookup, int, int) {
	if mode == 1 {
		return NewStaticQEXTMDCTLookup96000(), 240, 3840
	}
	return NewStaticQEXTMDCTLookup48000(), 120, 1920
}

func qextMDCTCaseName(mode, shift, stride int) string {
	return fmt.Sprintf("mode%d_shift%d_stride%d", mode, shift, stride)
}
