//go:build amd64.v3 && !gopus_fixed_point

package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

var celtStereoSplitOracleHelper libopustest.HelperCache
var celtStereoSplitAllocSink celtNorm

func buildCELTStereoSplitV3Helper(variant libopustooling.LibopusReferenceVariant) (string, error) {
	cfg := libopustest.CHelperConfig{
		Label:       "CELT v3 stereo split",
		OutputBase:  "gopus_libopus_celt_stereo_split_v3",
		SourceFile:  "libopus_celt_stereo_split_v3_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		SIMDRef:     variant == libopustooling.LibopusReferenceSIMD,
		DeadStrip:   true,
	}
	if variant == libopustooling.LibopusReferenceScalar {
		cfg.ForceScalarRef = true
	}
	return celtStereoSplitOracleHelper.Path(func() (string, error) {
		return libopustest.BuildCHelper(cfg)
	})
}

func TestCELTV3StereoSplitMatchesLibopusKernel(t *testing.T) {
	requireCELTV3OracleTarget(t)
	variant := requireCELTStereoSplitOracleMode(t)
	if variant != libopustooling.LibopusReferenceScalar && variant != libopustooling.LibopusReferenceSIMD {
		t.Fatalf("paired CELT oracle variant=%s, want scalar or SIMD", variant)
	}
	libopustest.RequireOracle(t)

	helper, err := buildCELTStereoSplitV3Helper(variant)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT v3 stereo split", err)
	}
	rng := rand.New(rand.NewSource(0x53546572656f))
	separateWitnesses := 0
	for _, n := range []int{1, 3, 4, 17, 24, 35} {
		inputX := make([]float32, n)
		inputY := make([]float32, n)
		for i := range inputX {
			inputX[i] = celtStereoSplitOracleFloat(rng)
			inputY[i] = celtStereoSplitOracleFloat(rng)
		}

		payload := libopustest.NewOraclePayloadVersion("GSSI", 1, uint32(n))
		payload.Float32s(inputX...)
		payload.Float32s(inputY...)
		reader, err := libopustest.RunOracleVersion(helper, payload.Bytes(), "CELT v3 stereo split", "GSSO", 1)
		if err != nil {
			libopustest.HelperUnavailable(t, "CELT v3 stereo split", err)
		}
		if got := reader.Count(n); got != n {
			t.Fatalf("length %d C output count=%d", n, got)
		}
		wantX := make([]float32, n)
		wantY := make([]float32, n)
		for i := range wantX {
			wantX[i] = reader.Float32()
		}
		for i := range wantY {
			wantY[i] = reader.Float32()
		}
		if err := reader.Err(); err != nil {
			t.Fatalf("length %d C output: %v", n, err)
		}
		if err := reader.ExpectConsumed(); err != nil {
			t.Fatal(err)
		}

		gotX := make([]celtNorm, n)
		gotY := make([]celtNorm, n)
		separateX := make([]celtNorm, n)
		separateY := make([]celtNorm, n)
		for i := range inputX {
			gotX[i], separateX[i] = celtNorm(inputX[i]), celtNorm(inputX[i])
			gotY[i], separateY[i] = celtNorm(inputY[i]), celtNorm(inputY[i])
		}
		stereoSplitInto(gotX, gotY)
		stereoSplitScalar(separateX, separateY)
		for i := range inputX {
			if got, want := math.Float32bits(float32(gotX[i])), math.Float32bits(wantX[i]); got != want {
				t.Fatalf("length %d X[%d]=%08x want pinned C %08x", n, i, got, want)
			}
			if got, want := math.Float32bits(float32(gotY[i])), math.Float32bits(wantY[i]); got != want {
				t.Fatalf("length %d Y[%d]=%08x want pinned C %08x", n, i, got, want)
			}
			if variant == libopustooling.LibopusReferenceScalar &&
				(math.Float32bits(float32(separateX[i])) != math.Float32bits(wantX[i]) ||
					math.Float32bits(float32(separateY[i])) != math.Float32bits(wantY[i])) {
				separateWitnesses++
			}
		}
	}
	if variant == libopustooling.LibopusReferenceScalar && separateWitnesses == 0 {
		t.Fatal("inputs do not distinguish the prior separate MUL+ADD scalar path from the pinned C contraction")
	}
}

func celtStereoSplitOracleFloat(rng *rand.Rand) float32 {
	// Keep deterministic finite non-grid values spanning both signs and a wide
	// exponent range so rounded products can affect the subsequent contraction.
	bits := uint32(0x3e000000) + ((rng.Uint32() % 6) << 23) | (rng.Uint32() & 0x007fffff)
	if rng.Uint32()&1 != 0 {
		bits |= 0x80000000
	}
	return math.Float32frombits(bits)
}

func TestCELTV3StereoSplitZeroAllocs(t *testing.T) {
	x := make([]celtNorm, 35)
	y := make([]celtNorm, 35)
	for i := range x {
		x[i] = celtNorm(float32(i)*0.125 - 1.5)
		y[i] = celtNorm(0.75 - float32(i)*0.0625)
	}
	stereoSplitInto(x, y)
	allocs := testing.AllocsPerRun(100, func() {
		stereoSplitInto(x, y)
		celtStereoSplitAllocSink = x[0]
	})
	if allocs != 0 {
		t.Fatalf("stereoSplitInto allocations/run=%v want 0", allocs)
	}
}
