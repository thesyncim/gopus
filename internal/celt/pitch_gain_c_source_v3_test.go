//go:build linux && amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

var celtPitchGainSourceHelper libopustest.HelperCache
var celtPitchGainAllocationSink float32

const celtPitchSourceSHA256 = "ad93368ae01b6fcadd28905e6ed5a55ad165ae5031654540af77d1ffe55acfd5"

type celtPitchGainSourceCase struct {
	name string
	xy   float32
	xx   float32
	yy   float32
}

func buildCELTPitchGainSourceHelper() (string, error) {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	cfg := libopustest.CHelperConfig{
		Label:       "CELT pitch gain original source",
		OutputBase:  "gopus_libopus_celt_pitch_gain_source",
		SourceFile:  "libopus_celt_pitch_gain_source.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk"},
		SIMDRef:     variant == libopustooling.LibopusReferenceSIMD,
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	}
	return libopustest.BuildCHelper(cfg)
}

func TestCELTPitchGainMatchesOriginalFloatSource(t *testing.T) {
	requireCELTV3OracleTarget(t)
	variant := requirePairedCELTOracleMode(t)
	libopustest.RequireOracle(t)
	sourcePath := libopustest.RefPath("celt", "pitch.c")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read selected pinned pitch.c %s: %v", sourcePath, err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(source)); got != celtPitchSourceSHA256 {
		t.Fatalf("selected pitch.c source SHA256=%s, want pinned %s", got, celtPitchSourceSHA256)
	}

	cases := []celtPitchGainSourceCase{
		{name: "ordinary", xy: 0.75, xx: 0.625, yy: 0.875},
		{name: "zero_xx_nonzero_xy", xy: 0.75, xx: 0, yy: 4},
		{name: "zero_yy_nonzero_xy", xy: 0.75, xx: 4, yy: 0},
		{name: "negative_zero_xy", xy: math.Float32frombits(0x80000000), xx: 4, yy: 9},
		{name: "negative_zero_xx", xy: 0.5, xx: math.Float32frombits(0x80000000), yy: 2},
		{name: "negative_zero_yy", xy: 0.5, xx: 2, yy: math.Float32frombits(0x80000000)},
		{name: "subnormal_xx", xy: 0.5, xx: math.Float32frombits(1), yy: 1},
		{name: "underflowed_product", xy: 0.75, xx: math.Float32frombits(0x0d800000), yy: math.Float32frombits(0x0d800000)},
		// The product rounds to 0x3e00007c, whose separate addition ties upward
		// to 0x3f900010. The exact product is just below that midpoint, so the
		// fused C expression rounds the denominator to 0x3f90000f instead; the
		// resulting gain bits differ after sqrt and division.
		{name: "denominator_fma_rounding", xy: 1, xx: math.Float32frombits(0x3f390062), yy: math.Float32frombits(0x3e312021)},
	}

	helper, err := celtPitchGainSourceHelper.Path(buildCELTPitchGainSourceHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT pitch gain original source", err)
	}
	payload := libopustest.NewOraclePayloadVersion("GCPG", 1, uint32(len(cases)))
	for _, tc := range cases {
		payload.Float32s(tc.xy, tc.xx, tc.yy)
	}
	data, err := libopustest.RunHelper(helper, payload.Bytes())
	if err != nil {
		t.Fatalf("run original %s compute_pitch_gain: %v", variant, err)
	}
	reader, err := libopustest.NewOracleReader("CELT pitch gain original source", "GPGO", data)
	if err != nil {
		t.Fatal(err)
	}
	if got := reader.Count(len(cases)); got != len(cases) {
		t.Fatalf("C case count=%d, want %d", got, len(cases))
	}

	var mismatches []string
	for _, tc := range cases {
		want := reader.Float32()
		got := computePitchGain(tc.xy, tc.xx, tc.yy)
		gotBits, wantBits := math.Float32bits(got), math.Float32bits(want)
		if gotBits != wantBits {
			mismatches = append(mismatches, fmt.Sprintf("%s Go=%08x C=%08x", tc.name, gotBits, wantBits))
		}
	}
	if err := reader.Err(); err != nil {
		t.Fatal(err)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 0 {
		t.Fatalf("compute_pitch_gain mismatches: %v", mismatches)
	}
	t.Logf("matched original %s float compute_pitch_gain for %d operand triples", variant, len(cases))
}

func TestCELTPitchGainWarmNoAllocs(t *testing.T) {
	const xy, xx, yy = float32(0.75), float32(0.625), float32(0.875)
	celtPitchGainAllocationSink = computePitchGain(xy, xx, yy)
	allocs := testing.AllocsPerRun(100, func() {
		celtPitchGainAllocationSink = computePitchGain(xy, xx, yy)
	})
	if allocs != 0 {
		t.Fatalf("warm computePitchGain allocations/run=%g, want 0", allocs)
	}
}
