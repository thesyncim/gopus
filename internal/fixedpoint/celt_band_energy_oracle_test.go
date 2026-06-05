//go:build gopus_fixed_point

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestCeltSqrt32Oracle checks CeltSqrt32 against the libopus FIXED_POINT
// celt_sqrt32 kernel bit-for-bit. celt_sqrt32 is the kernel compute_band_energies
// uses to turn an accumulated Q31 energy into a band amplitude. It requires a
// --enable-fixed-point libopus reference build (built on demand by the oracle
// harness).
func TestCeltSqrt32Oracle(t *testing.T) {
	libopustest.RequireOracle(t)

	inputs := celtSqrt32OracleInputs()
	words := make([]uint32, len(inputs))
	for i, x := range inputs {
		words[i] = uint32(x)
	}

	got, err := libopustest.ProbeCELTFixedMathWords(libopustest.CELTFixedMathModeSqrt32, words)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt fixed math", err)
		return
	}
	if len(got) != len(inputs) {
		t.Fatalf("oracle returned %d results, want %d", len(got), len(inputs))
	}

	for i, x := range inputs {
		want := int32(got[i])
		have := CeltSqrt32(x)
		if have != want {
			t.Errorf("CeltSqrt32(%d) = %d, libopus celt_sqrt32 = %d", x, have, want)
		}
	}
}

// TestCeltRsqrtNorm32Oracle checks CeltRsqrtNorm32 against the libopus
// FIXED_POINT celt_rsqrt_norm32 kernel bit-for-bit. It is the reciprocal-sqrt
// dependency of celt_sqrt32, valid for Q31 inputs in [0.25, 1).
func TestCeltRsqrtNorm32Oracle(t *testing.T) {
	libopustest.RequireOracle(t)

	inputs := celtRsqrtNorm32OracleInputs()
	words := make([]uint32, len(inputs))
	for i, x := range inputs {
		words[i] = uint32(x)
	}

	got, err := libopustest.ProbeCELTFixedMathWords(libopustest.CELTFixedMathModeRsqrtNorm32, words)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt fixed math", err)
		return
	}
	if len(got) != len(inputs) {
		t.Fatalf("oracle returned %d results, want %d", len(got), len(inputs))
	}

	for i, x := range inputs {
		want := int32(got[i])
		have := CeltRsqrtNorm32(x)
		if have != want {
			t.Errorf("CeltRsqrtNorm32(%d) = %d, libopus celt_rsqrt_norm32 = %d", x, have, want)
		}
	}
}

// celtSqrt32OracleInputs builds a deterministic non-negative input set covering
// the special cases, the saturation boundary, every ilog2/k bucket, a dense
// low-magnitude sweep, and a strided sweep across the full non-negative range.
func celtSqrt32OracleInputs() []int32 {
	seen := make(map[int32]struct{})
	var inputs []int32
	add := func(x int32) {
		if x < 0 {
			return
		}
		if _, ok := seen[x]; ok {
			return
		}
		seen[x] = struct{}{}
		inputs = append(inputs, x)
	}

	// Special cases and the saturation boundary.
	add(0)
	add(1)
	add(1073741824)     // saturates to 2^31-1
	add(1073741823)     // just below saturation
	add(1073741824 + 1) // above saturation
	add(2147483647)     // INT32_MAX, saturates

	// Every power-of-two and its neighbours hit each ilog2/k bucket.
	for shift := 0; shift < 31; shift++ {
		p := int32(1) << shift
		add(p)
		add(p - 1)
		add(p + 1)
	}

	// Dense low-magnitude sweep.
	for x := int32(0); x <= 4096; x++ {
		add(x)
	}

	// Strided sweep across the full non-negative range.
	for x := int64(0); x < int64(1)<<30; x += 0x10001 {
		add(int32(x))
	}

	return inputs
}

// celtRsqrtNorm32OracleInputs builds a deterministic input set for the Q31
// reciprocal-sqrt approximation. celt_sqrt32 only ever calls it with Q31 values
// in [0.25, 1) (i.e. [2^29, 2^31)); the sweep covers that band densely plus
// every representable boundary inside it.
func celtRsqrtNorm32OracleInputs() []int32 {
	const (
		lo = int32(1) << 29 // 0.25 in Q31
		hi = int32(2147483647)
	)
	seen := make(map[int32]struct{})
	var inputs []int32
	add := func(x int32) {
		if x < lo {
			return
		}
		if _, ok := seen[x]; ok {
			return
		}
		seen[x] = struct{}{}
		inputs = append(inputs, x)
	}

	add(lo)
	add(lo + 1)
	add(hi)
	add(hi - 1)
	add(int32(1) << 30) // 0.5 in Q31

	// Dense sweep at the bottom of the range where the polynomial is steepest.
	for x := lo; x <= lo+4096; x++ {
		add(x)
	}

	// Strided sweep across the whole [0.25, 1) band.
	for x := int64(lo); x < int64(hi); x += 0x4001 {
		add(int32(x))
	}

	return inputs
}
