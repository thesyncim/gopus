//go:build linux && amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

var libopusDualInnerProdSSEHelper libopustest.HelperCache

type dualInnerProdSSECase struct {
	x, y1, y2 []float32
}

func buildLibopusDualInnerProdSSEHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "CELT dual inner product SSE v3",
		OutputBase:  "gopus_libopus_celt_dual_inner_prod_sse",
		SourceFile:  "libopus_celt_dual_inner_prod_sse_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		SIMDRef:     true,
		Libs:        []string{libopustest.SIMDRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestPrefilterDualInnerProdV3MatchesLinkedLibopus(t *testing.T) {
	requireCELTV3OracleTarget(t)
	if variant := requirePairedCELTOracleMode(t); variant != libopustooling.LibopusReferenceSIMD {
		t.Fatalf("Go v3 SIMD dual inner product requires the matching libopus SIMD reference, got %s", variant)
	}
	if !prefilterDualInnerProdSSEUsesFMA {
		t.Fatal("AMD64 v3 SIMD dual inner product did not select the contracted kernel")
	}
	libopustest.RequireOracle(t)

	cases := dualInnerProdSSECorpus()
	payload := libopustest.NewOraclePayload("GDPI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.Float32s(tc.x...)
		payload.Float32s(tc.y1...)
		payload.Float32s(tc.y2...)
	}
	helper, err := libopusDualInnerProdSSEHelper.Path(buildLibopusDualInnerProdSSEHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT dual inner product SSE v3", err)
	}
	data, err := libopustest.RunHelper(helper, payload.Bytes())
	if err != nil {
		t.Fatalf("run original linked dual_inner_prod_sse: %v", err)
	}
	reader, err := libopustest.NewOracleReader("CELT dual inner product SSE v3", "GDPO", data)
	if err != nil {
		t.Fatal(err)
	}
	if got := reader.Count(len(cases)); got != len(cases) {
		t.Fatalf("C case count=%d want %d", got, len(cases))
	}
	for caseIndex, tc := range cases {
		want1, want2 := reader.Float32(), reader.Float32()
		got1, got2 := prefilterDualInnerProdF32SSEOrder(tc.x, tc.y1, tc.y2, len(tc.x))
		if math.Float32bits(got1) != math.Float32bits(want1) || math.Float32bits(got2) != math.Float32bits(want2) {
			t.Fatalf("n=%d case=%d Go=(%08x,%08x) linked C=(%08x,%08x)", len(tc.x), caseIndex,
				math.Float32bits(got1), math.Float32bits(got2), math.Float32bits(want1), math.Float32bits(want2))
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	t.Logf("matched original linked dual_inner_prod_sse for %d float32 operand cases", len(cases))
}

func dualInnerProdSSECorpus() []dualInnerProdSSECase {
	lengths := []int{1, 3, 4, 5, 7, 8, 15, 16, 59, 60, 61, 63, 64, 65, 120, 239, 240, 241}
	cases := make([]dualInnerProdSSECase, 0, len(lengths)*2)
	rng := rand.New(rand.NewSource(0x44505353))
	for _, n := range lengths {
		for pattern := range 2 {
			tc := dualInnerProdSSECase{x: make([]float32, n), y1: make([]float32, n), y2: make([]float32, n)}
			for i := range tc.x {
				if pattern == 0 {
					tc.x[i] = (rng.Float32()*2 - 1) * float32(math.Ldexp(1, rng.Intn(17)-8))
					tc.y1[i] = (rng.Float32()*2 - 1) * float32(math.Ldexp(1, rng.Intn(17)-8))
					tc.y2[i] = (rng.Float32()*2 - 1) * float32(math.Ldexp(1, rng.Intn(17)-8))
					continue
				}
				sign := float32(1)
				if i&1 != 0 {
					sign = -1
				}
				tc.x[i] = sign * (0.75 + float32(i%7)*0.03125)
				tc.y1[i] = sign * (0.5 - float32(i%5)*0.015625)
				tc.y2[i] = -sign * (0.375 + float32(i%3)*0.0625)
			}
			cases = append(cases, tc)
		}
	}
	return cases
}

func TestPrefilterDualInnerProdV3NoAllocs(t *testing.T) {
	x, y1, y2 := make([]float32, 60), make([]float32, 60), make([]float32, 60)
	for i := range x {
		x[i] = float32(i%11) * 0.0625
		y1[i] = float32(i%7) * -0.03125
		y2[i] = float32(i%5) * 0.125
	}
	_, _ = prefilterDualInnerProdF32SSEOrder(x, y1, y2, len(x))
	allocs := testing.AllocsPerRun(100, func() {
		dualInnerProdBenchSink, _ = prefilterDualInnerProdF32SSEOrder(x, y1, y2, len(x))
	})
	if allocs != 0 {
		t.Fatalf("steady-state allocations=%v, want 0", allocs)
	}
}
