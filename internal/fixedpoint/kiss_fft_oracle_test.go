//go:build gopus_fixedpoint

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestKFBfly2Oracle checks KFBfly2 against the libopus FIXED_POINT kf_bfly2
// radix-2 butterfly bit-for-bit. It requires a --enable-fixed-point libopus
// reference build (built on demand by the oracle harness).
func TestKFBfly2Oracle(t *testing.T) {
	libopustest.RequireOracle(t)

	groups := kfBfly2OracleGroups()
	in := make([]libopustest.KissFFTComplex, 0, groups*8)
	for _, s := range kfBfly2OracleSamples(groups) {
		in = append(in, libopustest.KissFFTComplex{R: s.R, I: s.I})
	}

	got, err := libopustest.ProbeKissFFTBfly2(in)
	if err != nil {
		libopustest.HelperUnavailable(t, "kiss fft", err)
		return
	}
	if len(got) != len(in) {
		t.Fatalf("oracle returned %d samples, want %d", len(got), len(in))
	}

	have := make([]FFTCpx, len(in))
	for i, s := range in {
		have[i] = FFTCpx{R: s.R, I: s.I}
	}
	KFBfly2(have, 0, groups)

	for i := range have {
		want := FFTCpx{R: got[i].R, I: got[i].I}
		if have[i] != want {
			t.Errorf("sample %d: KFBfly2 = {%d,%d}, libopus kf_bfly2 = {%d,%d}",
				i, have[i].R, have[i].I, want.R, want.I)
		}
	}
}

// kfBfly2OracleGroups returns the number of 8-sample groups exercised.
func kfBfly2OracleGroups() int { return 4096 }

// kfBfly2OracleSamples builds a deterministic input set covering structural
// edge cases (zeros, equal real/imag pairs that stress the rounding direction
// of the Q15 twiddle multiply, saturation-magnitude operands that exercise the
// wrapping ADD32_ovflw/SUB32_ovflw/NEG32_ovflw) plus a pseudo-random sweep.
func kfBfly2OracleSamples(groups int) []FFTCpx {
	out := make([]FFTCpx, 0, groups*8)

	extremes := []int32{
		0, 1, -1, 2, -2, 3, -3,
		32767, -32768, 65535, -65536,
		0x40000000, -0x40000000,
		0x7fffffff, -0x80000000,
		0x55555555, -0x55555555,
		0x12345678, -0x12345678,
		1 << 29, -(1 << 29), (1 << 30) - 1, 1 << 28,
	}

	push := func(r, i int32) { out = append(out, FFTCpx{R: r, I: i}) }

	// Deterministic structural samples: cross-product of extreme magnitudes,
	// padded to whole groups.
	for _, r := range extremes {
		for _, i := range extremes {
			push(r, i)
		}
	}

	rng := rand.New(rand.NewSource(0x6b66626c79320001))
	for len(out) < groups*8 {
		push(int32(rng.Uint32()), int32(rng.Uint32()))
	}
	return out[:groups*8]
}
