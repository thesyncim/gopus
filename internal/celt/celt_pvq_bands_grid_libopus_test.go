// Package celt — exhaustive PVQ/bands byte-parity grid vs libopus 1.6.1.
//
// This test file covers the full PVQ/bands byte grid. It drives the gopus standalone
// CELT encoder and the pinned libopus C oracle (libopus_celt_encode_info.c,
// GCGI/GCGO magic) across the full configuration space:
//
//	bandwidth   : NB / MB / WB / SWB / FB (end_band 13/15/17/19/21)
//	frame sizes : 2.5 ms / 5 ms / 10 ms / 20 ms (120/240/480/960 samples)
//	channels    : mono, stereo
//	bitrates    : spread that exercises very-low-K (sparse PVQ), mid-K,
//	              high-K (dense PVQ), plus folding-dominated and
//	              intensity-stereo threshold regions
//	signals     : tonal (exercises spread/rotation), transient (short-block
//	              MDCT, tf_select), wideband noise (folding, intra energy)
//
// A cell passes when it is byte-exact OR decode-identical (the only byte
// difference is benign range-coder trailing free bits, so both packets decode to
// bit-identical PCM). Every cell is byte-exact on amd64 (CI hard gate). On
// darwin/arm64 a handful of high-K cells byte-differ and decode within the
// documented CELT float FMA budget (≤1 ULP per op, project_arm64_celt_1ulp_drift.md);
// any larger decode divergence is a real coding bug and fails on every arch.
//
// Reference paths exercised here:
//
//	celt/rate.c    interp_bits2pulses — per-band K allocation
//	celt/bands.c   quant_all_bands / quant_band / alg_quant — PVQ encode
//	celt/vq.c      alg_quant / exp_rotation — PVQ search + rotation
//	celt/bands.c   folding / spread decision (quant_band_n=1 and skip paths)
//	celt/bands.c   intensity_stereo / dual_stereo coupling gates
//	celt/tf.c      tf_analysis — transient/tf_select selection
package celt

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// pvqGridHelper is the shared HelperCache for the PVQ/bands grid oracle.
var pvqGridHelper libopustest.HelperCache

// pvqGridHelperPath returns the path to the CELT encode helper that supports
// the GCGI/GCGO grid protocol (libopus_celt_encode_info.c).
func pvqGridHelperPath() (string, error) {
	return pvqGridHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "celt pvq grid",
		OutputBase:  "gopus_libopus_celt_pvq_grid",
		SourceFile:  "libopus_celt_encode_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "src", "include"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// pvqGridBandwidth describes one bandwidth dimension of the grid.
type pvqGridBandwidth struct {
	label   string
	bw      CELTBandwidth
	endBand uint32 // CELT_SET_END_BAND value (libopus src/opus_encoder.c)
}

// pvqGridSignal describes one signal class exercised in the grid.
type pvqGridSignal struct {
	label string
	pcmFn func(channels, frameSize int) []float32
}

// pvqGridCase is one cell in the exhaustive PVQ/bands grid.
type pvqGridCase struct {
	label     string
	channels  int
	frameSize int    // samples at 48 kHz
	bitrate   int32  // bits per second
	endBand   uint32 // CELT_SET_END_BAND
	bw        CELTBandwidth
	signal    string
	pcm       []float32
}

// pvqGridBandwidths lists all five CELT bandwidths with the end_band the gopus
// standalone CELT encoder uses for each (CELTBandwidth.EffectiveBands), so the
// libopus oracle is driven with the same coded-band count.
//
//	NB  → 13  (4 kHz)
//	MB  → 15  (CELTMediumband native CELT end_band)
//	WB  → 17  (8 kHz)
//	SWB → 19  (12 kHz)
//	FB  → 21  (20 kHz)
//
// The Opus top-level encoder maps both MB and WB to end_band 17, but the gopus
// standalone CELT encoder (which this grid drives directly via SetBandwidth)
// uses end_band 15 for CELTMediumband, so the oracle must match that count for
// an apples-to-apples comparison.
var pvqGridBandwidths = []pvqGridBandwidth{
	{"NB", CELTNarrowband, 13},
	{"MB", CELTMediumband, 15},
	{"WB", CELTWideband, 17},
	{"SWB", CELTSuperwideband, 19},
	{"FB", CELTFullband, 21},
}

// pvqGridFrameSizes lists all valid CELT frame sizes (samples at 48 kHz).
var pvqGridFrameSizes = []struct {
	label string
	size  int
}{
	{"2p5ms", 120},
	{"5ms", 240},
	{"10ms", 480},
	{"20ms", 960},
}

// pvqGridBitratesForBW returns a set of bitrates that exercise diverse K
// allocations for the given bandwidth and frame size.
//
// The selection targets:
//   - Very low: forces most bands to K=0 (folding-dominated, skip-band path)
//   - Low-mid:  a few bands get K=1–3 (sparse PVQ, alg_quant trivial paths)
//   - Mid:      typical allocation spread with K=3–20 per band
//   - High:     dense PVQ with K>20 in many bands (rotation path exercised)
//   - Very high (stereo only): exercises intensity-stereo threshold
func pvqGridBitratesForBW(endBand uint32, frameSize, channels int) []int32 {
	// Scale baselines by frame size (more samples → fewer kbps needed for same quality)
	// and channel count.
	fsScale := float64(frameSize) / 960.0 // normalize to 20ms
	chScale := float64(channels)

	// Base bitrates anchored to fullband mono 20ms, then scale down for
	// narrower bandwidths.
	var bwFraction float64
	switch endBand {
	case 13: // NB — 4 kHz
		bwFraction = 0.25
	case 17: // MB/WB — 6-8 kHz
		bwFraction = 0.45
	case 19: // SWB — 12 kHz
		bwFraction = 0.65
	default: // FB — 20 kHz
		bwFraction = 1.0
	}

	// Spread across 5 bitrates per dimension pair.
	// The effective per-frame byte budget governs K; we want to hit:
	//   very-low (folding), low (sparse), mid, high, dense.
	budgets := []float64{
		6000,  // very-low: folding, most bands skip or K=0
		16000, // low:  sparse PVQ, K=1-3 in low bands
		32000, // mid:  typical per-band K spread
		64000, // high: K>10 in most bands, rotation active
		96000, // dense: K>20, spread=SPREAD_AGGRESSIVE
	}

	rates := make([]int32, len(budgets))
	for i, base := range budgets {
		r := base * bwFraction * chScale / fsScale
		// Round to nearest 400 bps.
		r = math.Round(r/400) * 400
		// Clamp to [6000, 510000]. The gopus standalone CELT encoder clamps the
		// CBR bitrate to >=6000 inside cbrPayloadBytes; below that gopus pads to
		// the 6000-derived byte budget while the raw celt_encode_with_ec oracle
		// would use the smaller budget, producing different packet lengths that
		// are not a like-with-like comparison.
		if r < 6000 {
			r = 6000
		}
		if r > 510000 {
			r = 510000
		}
		rates[i] = int32(r)
	}
	return rates
}

// pvqTonalPCM generates a deterministic tonal signal (440 Hz + harmonics)
// that exercises the spread/rotation path in alg_quant.
func pvqTonalPCM(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		s := 0.42*math.Sin(2*math.Pi*440*float64(i)/48000) +
			0.18*math.Sin(2*math.Pi*880*float64(i)/48000) +
			0.07*math.Sin(2*math.Pi*1760*float64(i)/48000+0.5)
		pcm[i*channels] = float32(s)
		if channels == 2 {
			r := 0.35*math.Sin(2*math.Pi*523*float64(i)/48000+0.3) +
				0.12*math.Sin(2*math.Pi*1046*float64(i)/48000)
			pcm[i*channels+1] = float32(r)
		}
	}
	return pcm
}

// pvqTransientPCM generates a quiet-then-burst signal to exercise
// the transient analysis / short-block MDCT path (tf_select=1).
func pvqTransientPCM(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	onset := frameSize / 2
	burstLen := min(40, frameSize-onset)
	for i := range frameSize {
		amp := 0.02
		if i >= onset && i < onset+burstLen {
			amp = 0.75
		}
		l := amp * math.Sin(2*math.Pi*800*float64(i)/48000)
		pcm[i*channels] = float32(l)
		if channels == 2 {
			r := amp * math.Sin(2*math.Pi*1200*float64(i)/48000+0.2)
			pcm[i*channels+1] = float32(r)
		}
	}
	return pcm
}

// pvqNoisePCM generates deterministic pseudo-random noise to exercise
// folding and intra-energy paths.
func pvqNoisePCM(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	var state uint32 = 0xDEADBEEF
	for i := range pcm {
		state = state*1664525 + 1013904223
		pcm[i] = float32(int32(state)) / float32(math.MaxInt32) * 0.45
	}
	return pcm
}

// pvqMixedPCM combines tonal + noise to exercise the intensity-stereo
// and spread decisions in stereo cells.
func pvqMixedPCM(channels, frameSize int) []float32 {
	tonal := pvqTonalPCM(channels, frameSize)
	noise := pvqNoisePCM(channels, frameSize)
	out := make([]float32, frameSize*channels)
	for i := range out {
		out[i] = 0.6*tonal[i] + 0.35*noise[i]
	}
	return out
}

// pvqGridSignals lists signal classes. Each exercises different PVQ/bands paths.
var pvqGridSignals = []pvqGridSignal{
	{"tonal", pvqTonalPCM},
	{"transient", pvqTransientPCM},
	{"noise", pvqNoisePCM},
	{"mixed", pvqMixedPCM},
}

// buildPVQGrid returns the full exhaustive grid of test cases.
func buildPVQGrid() []pvqGridCase {
	var cases []pvqGridCase
	for _, bw := range pvqGridBandwidths {
		for _, fs := range pvqGridFrameSizes {
			for _, ch := range []int{1, 2} {
				chLabel := "mono"
				if ch == 2 {
					chLabel = "stereo"
				}
				bitrates := pvqGridBitratesForBW(bw.endBand, fs.size, ch)
				for _, br := range bitrates {
					for _, sig := range pvqGridSignals {
						label := fmt.Sprintf("%s-%s-%s-%dk-%s",
							bw.label, fs.label, chLabel, br/1000, sig.label)
						cases = append(cases, pvqGridCase{
							label:     label,
							channels:  ch,
							frameSize: fs.size,
							bitrate:   br,
							endBand:   bw.endBand,
							bw:        bw.bw,
							signal:    sig.label,
							pcm:       sig.pcmFn(ch, fs.size),
						})
					}
				}
			}
		}
	}
	return cases
}

// pvqGridTargetBytes mirrors cbrPayloadBytes for the oracle side so both
// encoder and oracle receive the same nbCompressedBytes budget.
func pvqGridTargetBytes(tc pvqGridCase) int {
	enc := newPVQGridEncoder(tc)
	return enc.cbrPayloadBytes(tc.frameSize)
}

// newPVQGridEncoder sets up a gopus CELT encoder matching the standalone
// configuration used by libopus_celt_encode_info.c:configure_encoder().
func newPVQGridEncoder(tc pvqGridCase) *Encoder {
	enc := NewEncoder(tc.channels)
	// Disable Opus top-level preprocessing stages (same as existing libopus
	// oracle tests; configure_encoder in the C oracle does not call them).
	enc.SetDCRejectEnabled(false)
	enc.SetLSBQuantizationEnabled(false)
	enc.SetDelayCompensationEnabled(false)
	enc.SetVBR(false)
	enc.SetConstrainedVBR(false)
	enc.SetComplexity(10)
	enc.SetBitrate(int(tc.bitrate))
	enc.SetBandwidth(tc.bw)
	return enc
}

// probeLibopusCELTPVQGrid calls the libopus GCGI/GCGO grid oracle for a
// slice of grid cases. Returns one packet []byte per case.
func probeLibopusCELTPVQGrid(binPath string, cases []pvqGridCase) ([][]byte, error) {
	payload := libopustest.NewOraclePayload("GCGI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.channels))
		payload.U32(uint32(tc.frameSize))
		payload.U32(uint32(pvqGridTargetBytes(tc)))
		payload.I32(tc.bitrate)
		payload.I32(10) // complexity=10
		payload.U32(tc.endBand)
		for _, sample := range tc.pcm {
			payload.Float32(sample)
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt pvq grid", "GCGO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]byte, count)
	for i := range count {
		n := int(reader.U32())
		out[i] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// pvqGridDecode decodes a standalone CELT packet for one grid cell with the
// decoder configured to match the encoder (same channels and bandwidth/end_band).
func pvqGridDecode(tc pvqGridCase, packet []byte) ([]float32, error) {
	dec := NewDecoder(tc.channels)
	dec.SetBandwidth(tc.bw)
	return dec.DecodeFrame(append([]byte(nil), packet...), tc.frameSize)
}

// pvqGridArm64FMABudget bounds the decoded-PCM divergence that the documented
// darwin/arm64 CELT float FMA contraction (project_arm64_celt_1ulp_drift.md) can
// introduce when the gopus float path contracts a*b+c into FMADD where the scalar
// libopus reference (clang, no contraction) does not. That sub-ULP-per-op drift
// can flip a single high-K PVQ pulse bit, so the packet bytes differ and the
// decoded PCM differs by a tiny amount (<=~1e-3 across the observed grid). On
// amd64 (the CI hard gate) the float path is bit-exact, so this budget is never
// consulted there. The budget sits well below any real coding divergence: a
// genuine encoder/decoder desync (e.g. an allocation or tf_encode mismatch)
// changes the per-band K and produces structural decode errors an order of
// magnitude larger, so it still fails on every architecture.
const pvqGridArm64FMABudget = 2.0e-3

// pvqGridCellOutcome classifies one grid cell against the libopus oracle packet.
//   - byteExact: gopus and libopus produced identical packet bytes.
//   - decodeIdentical: bytes differ but both packets decode (through the gopus
//     decoder) to bit-identical PCM. This covers benign range-coder trailing
//     free bits (ec_enc_done padding), which never affect the decoded signal.
//   - arm64FMAResidual: darwin/arm64 only — bytes differ and the decoded PCM
//     differs only within pvqGridArm64FMABudget (the documented CELT FMA budget).
//
// Anything else is a real divergence and fails the cell on every architecture.
type pvqGridCellOutcome struct {
	byteExact        bool
	decodeIdentical  bool
	arm64FMAResidual bool
	firstDiff        int
	gotLen           int
	wantLen          int
	pcmMaxDiff       float64
	pcmDiffAt        int
	decodeErr        error
}

func classifyPVQGridCell(tc pvqGridCase, got, ref []byte) pvqGridCellOutcome {
	out := pvqGridCellOutcome{gotLen: len(got), wantLen: len(ref), firstDiff: -1, pcmDiffAt: -1}
	if bytes.Equal(got, ref) {
		out.byteExact = true
		return out
	}

	lim := min(len(ref), len(got))
	for j := 0; j < lim; j++ {
		if got[j] != ref[j] {
			out.firstDiff = j
			break
		}
	}
	if out.firstDiff < 0 {
		out.firstDiff = lim // pure length mismatch
	}

	// Bytes differ: the only acceptable reason is benign trailing free bits, so
	// require that decoding both packets yields bit-identical PCM.
	pcmGot, errGot := pvqGridDecode(tc, got)
	pcmRef, errRef := pvqGridDecode(tc, ref)
	if errGot != nil || errRef != nil {
		out.decodeErr = errors.Join(errGot, errRef)
		return out
	}
	if len(pcmGot) != len(pcmRef) {
		out.pcmMaxDiff = math.Inf(1)
		return out
	}
	identical := true
	for j := range pcmGot {
		d := math.Abs(float64(pcmGot[j]) - float64(pcmRef[j]))
		if d > out.pcmMaxDiff {
			out.pcmMaxDiff = d
			out.pcmDiffAt = j
		}
		if pcmGot[j] != pcmRef[j] {
			identical = false
		}
	}
	out.decodeIdentical = identical
	if !identical && runtime.GOARCH == "arm64" && out.pcmMaxDiff <= pvqGridArm64FMABudget {
		out.arm64FMAResidual = true
	}
	return out
}

// TestCELTPVQBandsGridMatchesLibopus drives the full PVQ/bands configuration
// space through both gopus and the libopus oracle. A cell passes when it is
// byte-exact, OR decode-identical (the byte difference is only benign range-coder
// trailing free bits, so both packets decode to bit-identical PCM). There is no
// per-cell tolerance: a cell whose decoded PCM diverges is a real coding bug and
// fails on every architecture.
//
// The only architecture-specific allowance is the documented darwin/arm64 CELT
// float FMA contraction (pvqGridArm64FMABudget / project_arm64_celt_1ulp_drift.md):
// a handful of high-K cells byte-differ and decode with a sub-ULP delta. On amd64
// (the CI hard gate) the float path is bit-exact, so every cell is byte-exact or
// decode-identical there with no allowance.
//
// Grid dimensions:
//   - Bandwidth:  NB / MB / WB / SWB / FB (end_band 13/15/17/19/21)
//   - Frame size: 2.5 / 5 / 10 / 20 ms
//   - Channels:   mono, stereo
//   - Bitrates:   5 levels per (bw, fs, ch) covering sparse→dense PVQ
//   - Signals:    tonal, transient, noise, mixed
//
// References exercised:
//
//	celt/rate.c          interp_bits2pulses (per-band K allocation)
//	celt/bands.c         quant_all_bands / quant_band / alg_quant (PVQ encode)
//	celt/vq.c            alg_quant + op_pvq_search + exp_rotation
//	celt/bands.c         skip-band / folding path (low-K cells)
//	celt/bands.c         intensity_stereo gate (stereo cells)
//	celt/celt_encoder.c  tf_encode (entropy budget guard at minimum packet size)
func TestCELTPVQBandsGridMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	binPath, err := pvqGridHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt pvq grid", err)
		return
	}

	grid := buildPVQGrid()

	// Fetch all oracle packets in one shot (avoid per-case process overhead).
	wantPackets, err := probeLibopusCELTPVQGrid(binPath, grid)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt pvq grid oracle", err)
		return
	}

	type cellResult struct {
		label   string
		outcome pvqGridCellOutcome
		pass    bool
	}

	results := make([]cellResult, len(grid))

	t.Run("cells", func(t *testing.T) {
		for i, tc := range grid {
			ref := wantPackets[i]
			t.Run(tc.label, func(t *testing.T) {
				t.Parallel()
				enc := newPVQGridEncoder(tc)
				got, encErr := enc.EncodeFrame(append([]float32(nil), tc.pcm...), tc.frameSize)
				if encErr != nil {
					t.Fatalf("EncodeFrame: %v", encErr)
				}

				outcome := classifyPVQGridCell(tc, got, ref)
				pass := outcome.byteExact || outcome.decodeIdentical || outcome.arm64FMAResidual
				results[i] = cellResult{label: tc.label, outcome: outcome, pass: pass}
				if pass {
					if outcome.arm64FMAResidual {
						t.Logf("RESIDUAL arm64 CELT FMA drift: byte[%d] len got=%d want=%d "+
							"pcmMaxDiff=%g (<= %g budget); project_arm64_celt_1ulp_drift.md",
							outcome.firstDiff, outcome.gotLen, outcome.wantLen, outcome.pcmMaxDiff, pvqGridArm64FMABudget)
					}
					return
				}
				if outcome.decodeErr != nil {
					t.Errorf("FAIL byte[%d] len got=%d want=%d: decode error: %v",
						outcome.firstDiff, outcome.gotLen, outcome.wantLen, outcome.decodeErr)
					return
				}
				t.Errorf("FAIL byte[%d] len got=%d want=%d: decoded PCM diverges (maxDiff=%g at sample %d, budget=%g)",
					outcome.firstDiff, outcome.gotLen, outcome.wantLen, outcome.pcmMaxDiff, outcome.pcmDiffAt, pvqGridArm64FMABudget)
			})
		}
	})

	// Print per-cell summary table.
	byteExact, decodeOnly, residual, fail := 0, 0, 0, 0
	t.Logf("PVQ/Bands parity grid summary (%d cells):", len(grid))
	t.Logf("%-60s %6s %6s %10s", "Cell", "GotLen", "WntLen", "Status")
	for _, r := range results {
		switch {
		case r.outcome.byteExact:
			t.Logf("%-60s %6d %6d %10s", r.label, r.outcome.gotLen, r.outcome.wantLen, "BYTE-EXACT")
			byteExact++
		case r.outcome.decodeIdentical:
			t.Logf("%-60s %6d %6d %10s (trailing free bits @byte %d)",
				r.label, r.outcome.gotLen, r.outcome.wantLen, "DECODE-EQ", r.outcome.firstDiff)
			decodeOnly++
		case r.outcome.arm64FMAResidual:
			t.Logf("%-60s %6d %6d %10s (arm64 FMA pcmMaxDiff=%g @byte %d)",
				r.label, r.outcome.gotLen, r.outcome.wantLen, "RESIDUAL", r.outcome.pcmMaxDiff, r.outcome.firstDiff)
			residual++
		default:
			t.Logf("%-60s %6d %6d %10s (@byte %d, pcmMaxDiff=%g)",
				r.label, r.outcome.gotLen, r.outcome.wantLen, "FAIL", r.outcome.firstDiff, r.outcome.pcmMaxDiff)
			fail++
		}
	}
	t.Logf("---")
	t.Logf("byte-exact=%d decode-identical=%d arm64-fma-residual=%d fail=%d  arch=%s/%s",
		byteExact, decodeOnly, residual, fail, runtime.GOOS, runtime.GOARCH)

	if fail > 0 {
		t.Fatalf("PVQ/bands parity grid: %d/%d cells FAIL on %s/%s",
			fail, len(grid), runtime.GOOS, runtime.GOARCH)
	}
}

// TestCELTPVQBandsGridSummary is a summary-only variant that never fails the
// test but always prints the full per-cell table, useful for CI diff tracking
// without blocking builds.
func TestCELTPVQBandsGridSummary(t *testing.T) {
	libopustest.RequireOracle(t)

	binPath, err := pvqGridHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt pvq grid", err)
		return
	}

	grid := buildPVQGrid()
	t.Logf("Total cells: %d", len(grid))

	wantPackets, err := probeLibopusCELTPVQGrid(binPath, grid)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt pvq grid oracle", err)
		return
	}

	byteExact, decodeOnly, residual, fail := 0, 0, 0, 0
	for i, tc := range grid {
		enc := newPVQGridEncoder(tc)
		got, encErr := enc.EncodeFrame(append([]float32(nil), tc.pcm...), tc.frameSize)
		if encErr != nil {
			t.Errorf("cell %s EncodeFrame: %v", tc.label, encErr)
			fail++
			continue
		}
		outcome := classifyPVQGridCell(tc, got, wantPackets[i])
		switch {
		case outcome.byteExact:
			byteExact++
		case outcome.decodeIdentical:
			decodeOnly++
		case outcome.arm64FMAResidual:
			residual++
		default:
			fail++
		}
	}
	t.Logf("arch=%s/%s  byte-exact=%d  decode-identical=%d  arm64-fma-residual=%d  fail=%d  total=%d",
		runtime.GOOS, runtime.GOARCH, byteExact, decodeOnly, residual, fail, len(grid))
}
