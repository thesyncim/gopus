package hybrid

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// These oracles lock the hybrid-owned SILK+CELT band combine (the "float32 sum
// path") against the libopus 1.6.1 float build.
//
// In libopus the combine is not a standalone step: opus_decoder.c writes the
// SILK lowband into pcm and then calls celt_decode_with_ec_dred with
// celt_accum=1 (opus_decoder.c:370,607). The accumulation happens inside CELT's
// deemphasis():
//
//	y[j*C] = ADD_RES(y[j*C], SIG2RES(tmp));   // celt/celt_decoder.c:379
//
// For the float build (opus_res == float):
//
//	SIG2RES(a) = (1/CELT_SIG_SCALE)*a          // celt/arch.h:373
//	ADD_RES(a, b) = a + b                       // celt/arch.h:379
//	CELT_SIG_SCALE = 32768.f                    // celt/arch.h:57
//	SATURATE(x,a) = (x)                         // celt/arch.h:323 (no-op for float)
//
// gopus instead deemphasises+scales the CELT highband into a separate buffer
// (scale = 1/32768, matching SIG2RES) and then sums silk+celt in
// combineHybridBands. Because IEEE-754 float32 addition is commutative at the
// bit level, silk+celt and celt+silk are identical, so the gopus seam is
// bit-exact with the libopus accum combine. These tests prove that and guard
// against a future reordering (e.g. a fused multiply-add) breaking it.

const (
	combineModeDeemphasis = uint32(0)
	combinePreemphCoef    = float32(0.8500061035)
	combineCeltSigScale   = float32(1.0 / 32768.0)
)

var libopusHybridCombineHelper libopustest.HelperCache

func buildLibopusHybridCombineHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "hybrid combine",
		OutputBase:  "gopus_libopus_celt_filter",
		SourceFile:  "libopus_celt_filter_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DRESYNTH", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		RefSources:  []string{"celt/celt_decoder.c", "celt/celt.c"},
		Libs:        []string{"-lm"},
		DeadStrip:   true,
	})
}

// probeLibopusHybridCombine runs libopus deemphasis with accum=1 and a SILK seed,
// returning the combined pcm (silk + deemphasised/scaled celt) and final IIR mem.
func probeLibopusHybridCombine(t *testing.T, celtSig []float32, silkSeed []float32, mem float32) (pcm []float32, finalMem float32) {
	t.Helper()
	n := len(celtSig)
	binPath, err := libopusHybridCombineHelper.Path(buildLibopusHybridCombineHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "hybrid combine", err)
	}
	payload := libopustest.NewOraclePayload("GCFI", combineModeDeemphasis)
	payload.U32(1) // channels
	payload.U32(uint32(n))
	payload.U32(1) // downsample
	payload.U32(1) // accum
	payload.Float32(combinePreemphCoef)
	payload.Float32(0)
	payload.Float32(1)
	payload.Float32(1)
	payload.Float32(mem) // initial IIR mem
	for _, s := range celtSig {
		payload.Float32(s)
	}
	for _, s := range silkSeed {
		payload.Float32(s) // pcm seed = SILK lowband
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "hybrid combine", "GCFO")
	if err != nil {
		libopustest.HelperUnavailable(t, "hybrid combine", err)
	}
	if mode := reader.U32(); mode != combineModeDeemphasis {
		t.Fatalf("helper mode=%d want %d", mode, combineModeDeemphasis)
	}
	count := int(reader.U32())
	if count != n {
		t.Fatalf("helper count=%d want %d", count, n)
	}
	finalMem = reader.Float32()
	reader.ExpectRemaining(count * 4)
	pcm = make([]float32, count)
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return pcm, finalMem
}

// goHybridCombine reproduces the gopus hybrid pipeline: CELT deemphasis+scale
// into a separate buffer (matching celt.applyDeemphasisAndScaleToFloat32, which
// gopus drives for the hybrid highband) followed by combineHybridBands.
func goHybridCombine(celtSig []float32, silkSeed []float32, mem float32) (out []float32, finalMem float32) {
	n := len(celtSig)
	const verySmall float32 = 1e-30
	celtScaled := make([]float32, n)
	state := mem
	for i := 0; i < n; i++ {
		// gopus order: x + VERY_SMALL + m (celt/output_helpers.go). Proven
		// bit-identical to the libopus accum order x + m + VERY_SMALL for all
		// realistic magnitudes.
		tmp := celtSig[i] + verySmall + state
		state = combinePreemphCoef * tmp
		celtScaled[i] = tmp * combineCeltSigScale
	}
	out = make([]float32, n)
	combineHybridBands(out, celtScaled, silkSeed, n)
	return out, state
}

func assertCombineBitExact(t *testing.T, celtSig, silkSeed []float32, mem float32) {
	t.Helper()
	want, wantMem := probeLibopusHybridCombine(t, celtSig, silkSeed, mem)
	got, gotMem := goHybridCombine(celtSig, silkSeed, mem)
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("pcm[%d]=%08x %.10g want %08x %.10g (celt=%.6g silk=%.6g)",
				i, math.Float32bits(got[i]), got[i],
				math.Float32bits(want[i]), want[i], celtSig[i], silkSeed[i])
		}
	}
	if math.Float32bits(gotMem) != math.Float32bits(wantMem) {
		t.Fatalf("mem=%08x want %08x", math.Float32bits(gotMem), math.Float32bits(wantMem))
	}
}

func TestHybridBandCombineMatchesLibopusAccum(t *testing.T) {
	libopustest.RequireOracle(t)

	const n = 137
	celtSig := make([]float32, n)
	silkSeed := make([]float32, n)
	for i := range celtSig {
		// celt_sig domain values (pre-scale, ~ +/- 1e6 like a real highband)
		celtSig[i] = float32(math.Sin(float64(i+1)*0.137)*81000 + math.Cos(float64(i+7)*0.041)*23000)
		// SILK lowband at output scale (~ +/- 1.0)
		silkSeed[i] = float32(math.Sin(float64(i+3)*0.019)*0.31 + math.Cos(float64(i+11)*0.0073)*0.12)
	}
	assertCombineBitExact(t, celtSig, silkSeed, float32(-512.25))
}

func TestHybridBandCombineMatchesLibopusAccumZeroSilk(t *testing.T) {
	libopustest.RequireOracle(t)

	const n = 96
	celtSig := make([]float32, n)
	silkSeed := make([]float32, n) // all-zero SILK: pure CELT highband through accum
	for i := range celtSig {
		celtSig[i] = float32(math.Sin(float64(i+2)*0.211)*150000 - float64(i)*37.0)
	}
	assertCombineBitExact(t, celtSig, silkSeed, 0)
}

func TestHybridBandCombineMatchesLibopusAccumSmallMagnitudes(t *testing.T) {
	libopustest.RequireOracle(t)

	// Stress the leading near-silence region (where libopus emits exact zeros
	// and the VERY_SMALL term and add order could in principle matter).
	const n = 64
	celtSig := make([]float32, n)
	silkSeed := make([]float32, n)
	for i := range celtSig {
		celtSig[i] = float32(math.Sin(float64(i+1)*0.5) * 1e-3)
		silkSeed[i] = float32(math.Cos(float64(i+1)*0.3) * 5e-4)
	}
	assertCombineBitExact(t, celtSig, silkSeed, 0)
}
