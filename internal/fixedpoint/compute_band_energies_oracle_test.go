//go:build gopus_fixed_point

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// Standard CELT 5ms band layout and per-band log-N table (celt/modes.c
// eband5ms, celt/static_modes_fixed.h logN400). nbEBands = len(eband5ms)-1 = 21.
var (
	testEband5ms = []int16{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 14, 16, 20, 24, 28, 34, 40, 48, 60, 78, 100,
	}
	testLogN400 = []int16{
		0, 0, 0, 0, 0, 0, 0, 0, 8, 8, 8, 8, 16, 16, 16, 21, 21, 24, 29, 34, 36,
	}
)

// TestComputeBandEnergiesOracle checks ComputeBandEnergies against the real
// libopus FIXED_POINT compute_band_energies bit-for-bit, across the standard
// 960-sample MDCT mode at every time-resolution shift (LM 0..3) for mono and
// stereo, over signal sets that exercise silence, the per-band overflow shift,
// peak saturation, sign handling, and a deterministic pseudo-random sweep.
func TestComputeBandEnergiesOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		nbEBands      = 21
		shortMdctSize = 120
		end           = 21
	)

	type genFn func(n, c int) []int32
	gens := []struct {
		name string
		fn   genFn
	}{
		{"silence", func(n, c int) []int32 { return make([]int32, c*n) }},
		{"const_small", func(n, c int) []int32 {
			x := make([]int32, c*n)
			for i := range x {
				x[i] = 7
			}
			return x
		}},
		{"large_peaks", func(n, c int) []int32 {
			x := make([]int32, c*n)
			for i := range x {
				switch i % 5 {
				case 0:
					x[i] = 1 << 28
				case 1:
					x[i] = -(1 << 28)
				case 2:
					x[i] = 1<<30 - 1
				case 3:
					x[i] = -(1 << 30)
				default:
					x[i] = 0
				}
			}
			return x
		}},
		{"alternating", func(n, c int) []int32 {
			x := make([]int32, c*n)
			for i := range x {
				if i%2 == 0 {
					x[i] = 1 << 20
				} else {
					x[i] = -(1 << 20)
				}
			}
			return x
		}},
		{"prng", func(n, c int) []int32 {
			x := make([]int32, c*n)
			// Deterministic xorshift32 mapped into a wide signed range.
			state := uint32(0x9e3779b9)
			for i := range x {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				// Spread magnitudes: mask down to a per-index shift bucket so
				// different bands hit different overflow shifts.
				shift := uint(i % 24)
				x[i] = int32(state) >> shift
			}
			return x
		}},
	}

	for lm := 0; lm <= 3; lm++ {
		n := shortMdctSize << lm
		for _, C := range []int{1, 2} {
			for _, g := range gens {
				x := g.fn(n, C)

				want, err := libopustest.ProbeCELTFixedComputeBandEnergies(
					testEband5ms, testLogN400, x, nbEBands, shortMdctSize, end, C, lm)
				if err != nil {
					libopustest.HelperUnavailable(t, "celt fixed compute_band_energies", err)
					return
				}

				got := make([]int32, C*nbEBands)
				ComputeBandEnergies(x, testEband5ms, testLogN400, got, nbEBands, shortMdctSize, end, C, lm)

				if len(want) != len(got) {
					t.Fatalf("LM=%d C=%d %s: oracle returned %d energies, want %d",
						lm, C, g.name, len(want), len(got))
				}
				for i := range got {
					if got[i] != want[i] {
						t.Errorf("LM=%d C=%d %s: bandE[%d] = %d, libopus = %d",
							lm, C, g.name, i, got[i], want[i])
					}
				}
			}
		}
	}
}
