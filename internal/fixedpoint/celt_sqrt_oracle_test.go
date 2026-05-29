//go:build gopus_fixedpoint

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestCeltSqrtOracle checks CeltSqrt against the libopus FIXED_POINT celt_sqrt
// kernel bit-for-bit. It requires a --enable-fixed-point libopus reference build
// (built on demand by the oracle harness).
func TestCeltSqrtOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	inputs := celtSqrtOracleInputs()
	words := make([]uint32, len(inputs))
	for i, x := range inputs {
		words[i] = uint32(x)
	}

	got, err := libopustest.ProbeCELTFixedMathWords(libopustest.CELTFixedMathModeSqrt, words)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt fixed math", err)
		return
	}
	if len(got) != len(inputs) {
		t.Fatalf("oracle returned %d results, want %d", len(got), len(inputs))
	}

	for i, x := range inputs {
		want := int32(got[i])
		have := CeltSqrt(x)
		if have != want {
			t.Errorf("CeltSqrt(%d) = %d, libopus celt_sqrt = %d", x, have, want)
		}
	}
}

// celtSqrtOracleInputs builds a deterministic input set covering the special
// cases, every ilog2 bucket, dense low-magnitude values, and a strided sweep of
// the whole non-negative int32 range.
func celtSqrtOracleInputs() []int32 {
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
	add(1073741824)     // saturates to 32767
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
