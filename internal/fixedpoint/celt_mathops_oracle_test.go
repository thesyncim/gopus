//go:build gopus_fixedpoint

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// scalarOracle runs a single-input FIXED_POINT celt math kernel against the
// libopus reference and compares it to the supplied Go implementation.
func scalarOracle(t *testing.T, name string, mode uint32, inputs []int32, fn func(int32) int32) {
	t.Helper()
	libopustest.RequireOracle(t)

	words := make([]uint32, len(inputs))
	for i, x := range inputs {
		words[i] = uint32(x)
	}
	got, err := libopustest.ProbeCELTFixedMathWords(mode, words)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt fixed math", err)
		return
	}
	if len(got) != len(inputs) {
		t.Fatalf("%s: oracle returned %d results, want %d", name, len(got), len(inputs))
	}
	for i, x := range inputs {
		want := int32(got[i])
		have := fn(x)
		if have != want {
			t.Errorf("%s(%d) = %d, libopus = %d", name, x, have, want)
		}
	}
}

// TestCeltRcpOracle checks CeltRcp against libopus celt_rcp (x > 0).
func TestCeltRcpOracle(t *testing.T) {
	var inputs []int32
	for x := int32(1); x <= 4096; x++ {
		inputs = append(inputs, x)
	}
	for shift := 0; shift < 31; shift++ {
		p := int32(1) << shift
		inputs = append(inputs, p-1, p, p+1)
	}
	for x := int64(1); x < int64(1)<<31; x += 0x10001 {
		if int32(x) > 0 {
			inputs = append(inputs, int32(x))
		}
	}
	inputs = append(inputs, 2147483647)
	scalarOracle(t, "CeltRcp", libopustest.CELTFixedMathModeRcp, dedupePositive(inputs), CeltRcp)
}

// TestCeltRcpNorm16Oracle checks CeltRcpNorm16 against libopus celt_rcp_norm16
// over the full normalized Q15 input range [0, 32767].
func TestCeltRcpNorm16Oracle(t *testing.T) {
	var inputs []int32
	for x := int32(0); x <= 32767; x++ {
		inputs = append(inputs, x)
	}
	scalarOracle(t, "CeltRcpNorm16", libopustest.CELTFixedMathModeRcpNorm16, inputs,
		func(x int32) int32 { return int32(CeltRcpNorm16(int16(x))) })
}

// TestCeltRcpNorm32Oracle checks CeltRcpNorm32 against libopus celt_rcp_norm32
// over the asserted input range [2^30, 2^31).
func TestCeltRcpNorm32Oracle(t *testing.T) {
	const lo = int64(1) << 30
	const hi = int64(1) << 31
	var inputs []int32
	inputs = append(inputs, int32(lo), int32(hi-1))
	for x := lo; x < hi; x += 0x4001 {
		inputs = append(inputs, int32(x))
	}
	scalarOracle(t, "CeltRcpNorm32", libopustest.CELTFixedMathModeRcpNorm32, inputs, CeltRcpNorm32)
}

// TestCeltCosNormOracle checks CeltCosNorm against libopus celt_cos_norm.
func TestCeltCosNormOracle(t *testing.T) {
	var inputs []int32
	for x := int32(0); x <= (1 << 17); x++ {
		inputs = append(inputs, x)
	}
	// A few values outside the masked window to exercise the masking.
	for x := int64(0); x < int64(1)<<31; x += 0x9001 {
		inputs = append(inputs, int32(x))
	}
	scalarOracle(t, "CeltCosNorm", libopustest.CELTFixedMathModeCosNorm, inputs,
		func(x int32) int32 { return int32(CeltCosNorm(x)) })
}

// TestCeltCosNorm32Oracle checks CeltCosNorm32 against libopus celt_cos_norm32
// over the asserted input range [-2^30, 2^30] in Q30.
func TestCeltCosNorm32Oracle(t *testing.T) {
	const bound = int32(1) << 30
	var inputs []int32
	for x := -bound; ; x += 0x4001 {
		inputs = append(inputs, x)
		if x > bound-0x4001 {
			break
		}
	}
	inputs = append(inputs, -bound, 0, bound)
	scalarOracle(t, "CeltCosNorm32", libopustest.CELTFixedMathModeCosNorm32, inputs, CeltCosNorm32)
}

// TestFracDiv32Oracle checks FracDiv32 and FracDiv32Q29 against the libopus
// frac_div32 family (two int32 inputs per record, b > 0).
func TestFracDiv32Oracle(t *testing.T) {
	libopustest.RequireOracle(t)

	var as, bs []int32
	addPair := func(a, b int32) {
		if b <= 0 {
			return
		}
		as = append(as, a)
		bs = append(bs, b)
	}
	avals := []int32{0, 1, -1, 7, -7, 12345, -12345, 1 << 20, -(1 << 20),
		1 << 28, -(1 << 28), 1 << 30, -(1 << 30), 2147483647, -2147483647}
	for shift := 0; shift < 31; shift++ {
		b := int32(1) << shift
		for _, a := range avals {
			addPair(a, b)
			if b > 1 {
				addPair(a, b-1)
			}
			if b < (1 << 30) {
				addPair(a, b+1)
			}
		}
	}
	for b := int64(1); b < int64(1)<<31; b += 0x300001 {
		for _, a := range avals {
			addPair(a, int32(b))
		}
	}

	for _, m := range []struct {
		name string
		mode uint32
		fn   func(int32, int32) int32
	}{
		{"FracDiv32Q29", libopustest.CELTFixedMathModeFracDiv32Q29, FracDiv32Q29},
		{"FracDiv32", libopustest.CELTFixedMathModeFracDiv32, FracDiv32},
	} {
		got, err := libopustest.ProbeCELTFixedMathPairs(m.mode, as, bs)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt fixed math", err)
			return
		}
		if len(got) != len(as) {
			t.Fatalf("%s: oracle returned %d results, want %d", m.name, len(got), len(as))
		}
		for i := range as {
			want := int32(got[i])
			have := m.fn(as[i], bs[i])
			if have != want {
				t.Errorf("%s(%d, %d) = %d, libopus = %d", m.name, as[i], bs[i], have, want)
			}
		}
	}
}

func dedupePositive(in []int32) []int32 {
	seen := make(map[int32]struct{}, len(in))
	out := in[:0:0]
	for _, x := range in {
		if x <= 0 {
			continue
		}
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}
