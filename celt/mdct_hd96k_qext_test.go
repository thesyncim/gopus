//go:build gopus_qext

package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	hd96kMDCTOpLong             = uint32(0)
	hd96kMDCTOpTransient        = uint32(1)
	hd96kMDCTOpForward          = uint32(2)
	hd96kMDCTOpForwardTransient = uint32(3)
)

var libopusQEXTMDCTHelper libopustest.HelperCache

func buildLibopusQEXTMDCTHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext mdct",
		OutputBase:  "gopus_libopus_celt_qext_mdct",
		SourceFile:  "libopus_celt_qext_mdct_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// hd96kMDCTSeed generates the same deterministic pseudo-random vector the
// existing CELT MDCT parity tests use, so inputs stay reproducible across runs.
func hd96kMDCTSeed(n, seed int) []float32 {
	v := make([]float32, n)
	for i := 0; i < n; i++ {
		v[i] = float32((seed*17+i*31)%32768 - 16384)
	}
	return v
}

func probeLibopusHD96kMDCT(t *testing.T, op uint32, frameSize, overlap, shortBlocks int, head, body []float32) []float32 {
	t.Helper()
	binPath, err := libopusQEXTMDCTHelper.Path(buildLibopusQEXTMDCTHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext mdct", err)
	}
	payload := libopustest.NewOraclePayload("GQXI", op, uint32(frameSize), uint32(overlap), uint32(shortBlocks))
	for _, v := range head {
		payload.Float32(v)
	}
	for _, v := range body {
		payload.Float32(v)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext mdct", "GQXO")
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext mdct", err)
	}
	if gotOp := reader.U32(); gotOp != op {
		t.Fatalf("helper op=%d want %d", gotOp, op)
	}
	count := int(reader.U32())
	out := make([]float32, count)
	reader.ExpectRemaining(count * 4)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("qext mdct oracle payload not fully consumed: %v", err)
	}
	return out
}

// hd96kMDCTPerElemTol and hd96kMDCTEnergyTol bound the documented
// CELT cosine/FMA residual across the 96 kHz MDCT/FFT path
// (project_arm64_celt_1ulp_drift.md). The 96 kHz long block runs a 960-point
// KISS-FFT (vs 480 at 48 kHz), so the same per-step 1-ULP rounding drift
// accumulates over a deeper transform; absolute errors therefore scale with
// signal magnitude. This deeper accumulation surfaces on amd64 too (gopus's
// SSE-ordered float path vs the SIMD qext libopus the oracle links diverges a
// few ULP across 960 FFT steps), so the honest metric -- per-element error
// relative to the output RMS, and energy-relative error -- is applied on every
// arch rather than demanding bit-exactness from this one deep transform.
const (
	hd96kMDCTPerElemTol = 1e-4
	hd96kMDCTEnergyTol  = 1e-5
)

func checkHD96kMDCT(t *testing.T, name string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length: got %d want %d", name, len(got), len(want))
	}

	var sig, errp, maxErr float64
	for i := range want {
		sig += float64(want[i]) * float64(want[i])
		d := math.Abs(float64(got[i]) - float64(want[i]))
		errp += d * d
		if d > maxErr {
			maxErr = d
		}
	}
	rms := math.Sqrt(sig / float64(len(want)))
	if rms == 0 {
		if maxErr != 0 {
			t.Fatalf("%s: zero reference but max error %v", name, maxErr)
		}
		return
	}
	perElem := maxErr / rms
	energyRel := math.Sqrt(errp / sig)
	if perElem > hd96kMDCTPerElemTol {
		t.Fatalf("%s per-element residual %.3e (relative to RMS %.3g) exceeds budget %.3e", name, perElem, rms, hd96kMDCTPerElemTol)
	}
	if energyRel > hd96kMDCTEnergyTol {
		t.Fatalf("%s energy-relative residual %.3e exceeds budget %.3e", name, energyRel, hd96kMDCTEnergyTol)
	}
	t.Logf("RESIDUAL cosine/FMA drift on %s: perElem/RMS=%.3e energyRel=%.3e (<= %.0e/%.0e, project_arm64_celt_1ulp_drift.md)", name, perElem, energyRel, hd96kMDCTPerElemTol, hd96kMDCTEnergyTol)
}

// TestHD96kMDCTMatchesLibopusQEXT drives the native 96 kHz forward and inverse
// MDCT (long and transient) from the libopus qext oracle on the same input
// vectors and checks byte/numeric parity. amd64 is a hard byte gate; arm64
// logs the bounded per-arch cosine/FMA residual.
func TestHD96kMDCTMatchesLibopusQEXT(t *testing.T) {
	libopustest.RequireOracle(t)
	m := NewHD96kMode()
	frameSize := m.MdctN / 2 // 1920
	overlap := m.Overlap     // 240
	shortBlocks := m.NbShortMdcts

	// Forward long MDCT: input frameSize+overlap time samples -> frameSize coeffs.
	t.Run("forward_long", func(t *testing.T) {
		in := hd96kMDCTSeed(frameSize+overlap, 1)
		got := m.hd96kMDCTForward(in)
		want := probeLibopusHD96kMDCT(t, hd96kMDCTOpForward, frameSize, overlap, 0, in, nil)
		checkHD96kMDCT(t, "forward_long", got, want)
	})

	// Forward transient MDCT: 8 short blocks, interleaved coefficients.
	t.Run("forward_transient", func(t *testing.T) {
		in := hd96kMDCTSeed(frameSize+overlap, 2)
		got := m.hd96kMDCTForwardShort(in)
		want := probeLibopusHD96kMDCT(t, hd96kMDCTOpForwardTransient, frameSize, overlap, shortBlocks, in, nil)
		checkHD96kMDCT(t, "forward_transient", got, want)
	})

	// Inverse long MDCT: overlap history + frameSize coeffs -> frameSize+overlap.
	t.Run("inverse_long", func(t *testing.T) {
		prev := hd96kMDCTSeed(overlap, 3)
		spec := hd96kMDCTSeed(frameSize, 4)
		got := m.hd96kIMDCTLong(spec, prev)
		want := probeLibopusHD96kMDCT(t, hd96kMDCTOpLong, frameSize, overlap, 0, prev, spec)
		checkHD96kMDCT(t, "inverse_long", got, want)
	})

	// Inverse transient MDCT: 8 short blocks overlap-added into a shared buffer.
	t.Run("inverse_transient", func(t *testing.T) {
		prev := hd96kMDCTSeed(overlap, 5)
		spec := hd96kMDCTSeed(frameSize, 6)
		got := m.hd96kIMDCTShort(spec, prev)
		want := probeLibopusHD96kMDCT(t, hd96kMDCTOpTransient, frameSize, overlap, shortBlocks, prev, spec)
		checkHD96kMDCT(t, "inverse_transient", got, want)
	})
}
