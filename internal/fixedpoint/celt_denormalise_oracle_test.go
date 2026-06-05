//go:build gopus_fixed_point

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestDenormaliseBandsOracle checks DenormaliseBands against the real libopus
// FIXED_POINT denormalise_bands bit-for-bit, across the standard 5ms band layout
// at every time-resolution shift (LM 0..3), over a matrix of start/end ranges,
// downsample factors and the silence flag, with several normalized-coefficient
// and band-log-energy signal patterns that exercise the gain integer/fractional
// split, the negative-shift gain cap, and the shift>=31 zero-gain branch.
func TestDenormaliseBandsOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		nbEBands      = 21
		shortMdctSize = 120
		fullEnd       = 21
	)

	// Coefficient patterns over the normalized X buffer (length M*eBands[end]).
	type normGen func(n int) []int32
	normGens := []struct {
		name string
		fn   normGen
	}{
		{"zeros", func(n int) []int32 { return make([]int32, n) }},
		{"const_q24", func(n int) []int32 {
			x := make([]int32, n)
			for i := range x {
				x[i] = 1 << 22
			}
			return x
		}},
		{"alternating", func(n int) []int32 {
			x := make([]int32, n)
			for i := range x {
				if i%2 == 0 {
					x[i] = 1 << 23
				} else {
					x[i] = -(1 << 23)
				}
			}
			return x
		}},
		{"prng", func(n int) []int32 {
			x := make([]int32, n)
			state := uint32(0x12345678)
			for i := range x {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				x[i] = int32(state) >> uint(i%20)
			}
			return x
		}},
	}

	// Band-log-energy patterns over bandLogE (Q24, celt_glog). The values are
	// chosen to span ordinary gains, the negative-shift cap (large positive lg)
	// and the shift>=31 zero branch (very negative lg).
	type logEGen func(n int) []int32
	logEGens := []struct {
		name string
		fn   logEGen
	}{
		{"zero", func(n int) []int32 { return make([]int32, n) }},
		{"moderate", func(n int) []int32 {
			e := make([]int32, n)
			for i := range e {
				e[i] = int32(i-8) << 24 // -8.0 .. +12.0 in Q24
			}
			return e
		}},
		{"extreme_high", func(n int) []int32 {
			e := make([]int32, n)
			for i := range e {
				e[i] = 20 << 24 // triggers shift<0 gain cap
			}
			return e
		}},
		{"extreme_low", func(n int) []int32 {
			e := make([]int32, n)
			for i := range e {
				e[i] = -40 << 24 // triggers shift>=31 zero gain
			}
			return e
		}},
		{"prng", func(n int) []int32 {
			e := make([]int32, n)
			state := uint32(0x9e3779b9)
			for i := range e {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				// Spread roughly across [-32, +16] in Q24.
				e[i] = (int32(state>>20)%48 - 32) << 24
			}
			return e
		}},
	}

	type rangeCfg struct {
		start, end, downsample int
		silence                bool
	}
	ranges := []rangeCfg{
		{0, fullEnd, 1, false},
		{0, fullEnd, 2, false},
		{0, fullEnd, 4, false},
		{3, fullEnd, 1, false},
		{0, 12, 1, false},
		{5, 17, 2, false},
		{0, fullEnd, 1, true},
	}

	for lm := 0; lm <= 3; lm++ {
		M := 1 << lm
		n := M * shortMdctSize
		for _, rc := range ranges {
			xlen := M * int(testEband5ms[rc.end])
			for _, ng := range normGens {
				for _, eg := range logEGens {
					x := ng.fn(xlen)
					bandLogE := eg.fn(nbEBands)

					want, err := libopustest.ProbeCELTFixedDenormaliseBands(
						testEband5ms, bandLogE, x, nbEBands, shortMdctSize,
						rc.start, rc.end, M, rc.downsample, rc.silence)
					if err != nil {
						libopustest.HelperUnavailable(t, "celt fixed denormalise_bands", err)
						return
					}

					got := make([]int32, n)
					// Copy x so the kernel cannot observe shared mutation.
					xCopy := append([]int32(nil), x...)
					DenormaliseBands(xCopy, got, bandLogE, testEband5ms,
						shortMdctSize, rc.start, rc.end, M, rc.downsample, rc.silence)

					if len(want) != len(got) {
						t.Fatalf("LM=%d %v norm=%s logE=%s: oracle returned %d, want %d",
							lm, rc, ng.name, eg.name, len(want), len(got))
					}
					for i := range got {
						if got[i] != want[i] {
							t.Errorf("LM=%d %v norm=%s logE=%s: freq[%d] = %d, libopus = %d",
								lm, rc, ng.name, eg.name, i, got[i], want[i])
							break
						}
					}
				}
			}
		}
	}
}
