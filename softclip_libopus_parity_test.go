package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// isHalfIntegerTie reports whether x, scaled by 32768 in float32 (the libopus
// FLOAT2INT16 scale), lands exactly on a half-integer k+0.5. Such inputs are the
// only ones where round-half-to-even and round-half-away disagree, and they
// disagree by exactly one ULP of the integer result.
func isHalfIntegerTie(x float32) bool {
	y := x * 32768.0
	frac := y - float32(int32(y))
	return frac == 0.5 || frac == -0.5
}

func abs16Diff(a, b int16) int {
	d := int(a) - int(b)
	if d < 0 {
		return -d
	}
	return d
}

func assertSoftClipFloat32BitsEqual(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%g want %g", label, i, got[i], want[i])
		}
	}
}

func TestOpusPCMSoftClipMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name     string
		n        int
		channels int
		mem      []float32
		samples  []float32
	}{
		{
			name:     "mono clipped segments",
			n:        8,
			channels: 1,
			mem:      []float32{0},
			samples:  []float32{1.25, 1.5, 1.8, 1.4, 0.8, -0.2, -1.2, -1.6},
		},
		{
			name:     "stereo independent channels",
			n:        6,
			channels: 2,
			mem:      []float32{0, 0},
			samples:  []float32{0.2, -1.8, 1.4, -1.2, 1.7, -0.4, 0.6, 0.3, -0.5, 1.1, -1.3, 1.9},
		},
		{
			name:     "carryover memory",
			n:        6,
			channels: 1,
			mem:      []float32{-0.2},
			samples:  []float32{0.8, 0.4, -0.2, 1.4, 1.1, 0.3},
		},
		{
			name:     "hard clamp domain",
			n:        5,
			channels: 1,
			mem:      []float32{0},
			samples:  []float32{2.5, -2.5, 3, -3, 0},
		},
		{
			name:     "stereo carryover only",
			n:        4,
			channels: 2,
			mem:      []float32{-0.1, 0.12},
			samples:  []float32{0.5, -0.5, 0.25, -0.25, -0.1, 0.1, 0, 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, wantMem, err := libopustest.ProbeSoftClip(tc.n, tc.channels, tc.samples, tc.mem)
			if err != nil {
				libopustest.HelperUnavailable(t, "softclip", err)
			}
			got := append([]float32(nil), tc.samples...)
			gotMem := append([]float32(nil), tc.mem...)
			opusPCMSoftClip(got, tc.n, tc.channels, gotMem)
			assertSoftClipFloat32BitsEqual(t, got, want, "pcm")
			assertSoftClipFloat32BitsEqual(t, gotMem, wantMem, "mem")
		})
	}
}

func TestSoftClipAndFloat32ToInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name     string
		n        int
		channels int
		mem      []float32
		src      []float32
	}{
		{
			name:     "fast path in range zero memory",
			n:        8,
			channels: 2,
			mem:      []float32{0, 0},
			src: []float32{
				-1, 1,
				float32(-1.5 / 32768.0), float32(1.5 / 32768.0),
				float32(-0.5 / 32768.0), float32(0.5 / 32768.0),
				-0.75, 0.75,
				-0.125, 0.125,
				0, float32(32767.0 / 32768.0),
				float32(-32767.0 / 32768.0), float32(32766.5 / 32768.0),
				float32(-32766.5 / 32768.0), 0,
			},
		},
		{
			name:     "zero memory clipped input",
			n:        8,
			channels: 2,
			mem:      []float32{0, 0},
			src: []float32{
				1.3, -0.9,
				1.7, -1.8,
				0.9, -1.2,
				-0.1, 0.4,
				-1.4, 1.6,
				-1.9, 1.2,
				-0.6, 0.2,
				0.1, -0.1,
			},
		},
		{
			name:     "carryover clipped input",
			n:        8,
			channels: 2,
			mem:      []float32{-0.08, 0.11},
			src: []float32{
				1.3, -0.9,
				1.7, -1.8,
				0.9, -1.2,
				-0.1, 0.4,
				-1.4, 1.6,
				-1.9, 1.2,
				-0.6, 0.2,
				0.1, -0.1,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantFloat, wantMem, err := libopustest.ProbeSoftClip(tc.n, tc.channels, tc.src, tc.mem)
			if err != nil {
				libopustest.HelperUnavailable(t, "softclip", err)
			}
			want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeCELTDispatch, wantFloat)
			if err != nil {
				libopustest.HelperUnavailable(t, "float quant", err)
			}

			gotSrc := append([]float32(nil), tc.src...)
			gotMem := append([]float32(nil), tc.mem...)
			got := make([]int16, len(tc.src))
			softClipAndFloat32ToInt16(got, gotSrc, tc.n, tc.channels, gotMem)

			for i := range want {
				if got[i] == want[i] {
					continue
				}
				// gopus rounds float->int16 half-to-even (FCVTNS / lrintf under the
				// default IEEE rounding mode), matching scalar libopus. At an input
				// that scales to an exact half-integer (k+0.5)*1/32768, ties-to-even
				// and ties-away both produce a valid result one ULP apart. Apple's
				// NEON libm flips at that exact half-way point, so the macOS oracle
				// can return the ties-away neighbour for a tie input. Tolerate a
				// strict ±1 difference, but ONLY at an exact half-integer tie; every
				// non-tie sample must still match bit-for-bit.
				if isHalfIntegerTie(wantFloat[i]) && abs16Diff(got[i], want[i]) <= 1 {
					continue
				}
				t.Fatalf("sample[%d]=%d want %d (input=%0.10g)", i, got[i], want[i], wantFloat[i])
			}
			assertSoftClipFloat32BitsEqual(t, gotSrc, wantFloat, "softclipped pcm")
			assertSoftClipFloat32BitsEqual(t, gotMem, wantMem, "mem")
		})
	}
}
