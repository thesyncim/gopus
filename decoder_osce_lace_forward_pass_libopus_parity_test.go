//go:build gopus_osce

package gopus

import (
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
)

// TestOSCELACEForwardPassMatchesLibopus is the tight numerical parity
// probe for the gopus OSCE LACE / NoLACE postfilter forward pass.
//
// It mirrors the OSCE BWE parity test pattern (see
// `TestOSCEBWEForwardPassMatchesLibopusNumericalParity`): the libopus reference
// helper (`tools/csrc/libopus_osce_lace_forward.c`) is built against an
// OSCE-enabled libopus 1.6.1 build (`--enable-osce`) and the helper drives
// the static `lace_process_20ms_frame` / `nolace_process_20ms_frame`
// entries directly on a deterministic 1 kHz 16 kHz sinusoid. The gopus
// runtime is driven on the same input + same zero-features / zero-numbits /
// small-period inputs and the two 16 kHz 320-sample outputs are compared.
//
// Parity is near float32 roundoff but remains a numerical comparator: the feature-net trace
// matches libopus through conv2/tconv and stays at float epsilon after the GRU,
// while residual drift now comes from the AdaComb/AdaConv signal filters. See
// `cases` below for the active envelope.
func TestOSCELACEForwardPassMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := getLibopusOSCELACEForwardHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE LACE forward", err)
	}

	blob := requireLibopusOSCELACEModelBlob(t)
	parsed, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	if !parsed.SupportsOSCELACE() {
		t.Fatalf("parsed LACE blob does not satisfy SupportsOSCELACE()")
	}
	if !parsed.SupportsOSCENoLACE() {
		t.Fatalf("parsed LACE blob does not satisfy SupportsOSCENoLACE()")
	}
	model, err := osceLACE.Load(parsed)
	if err != nil {
		t.Fatalf("osceLACE.Load: %v", err)
	}

	const (
		inputLen = 320 // 20 ms @ 16 kHz: LACE/NoLACE restricted framing
	)

	// Same 1 kHz sinusoid the C helper generates: float[i] = round(0.5*sin)*1/32768.
	xq16 := make([]int16, inputLen)
	in := make([]float32, inputLen)
	for i := 0; i < inputLen; i++ {
		v := 0.5 * math.Sin(2*math.Pi*1000*float64(i)/16000)
		q := int(math.Round(v * 32767))
		if q > 32767 {
			q = 32767
		} else if q < -32768 {
			q = -32768
		}
		xq16[i] = int16(q)
		in[i] = float32(xq16[i]) / 32768
	}

	// Zero features + zero numbits + small period (matches the C helper).
	features := make([]float32, 4*93)
	numbits := []float32{0, 0}
	periods := []int{60, 60, 60, 60}

	// Per-mode tolerances. The helper build and Go runtime both use the scalar
	// DNN math path, so both are hard numerical contracts.
	//
	// LACE is a bit-exact oracle (maxAbs 0, rms 0 vs libopus).
	//
	// NoLACE is also a hard gate: matching libopus' scale_kernel norm (rounded,
	// non-fused squares) and the prebuilt sgemv FMA boundary (rows==2 gain
	// layers fuse, rows==1 round) brings the full forward pass to
	// maxAbs ~4.5e-8 / rms ~1.3e-8 vs libopus -- comfortably inside the
	// original 2e-7 / 5e-8 contract. The residual is sub-ULP AdaShape exp/log
	// transcendental noise, not signal-net drift.
	cases := []struct {
		name               string
		mode               string
		outputAbsTolerance float32
		outputRMSTolerance float64
	}{
		{"LACE", "lace", 1.5e-7, 5e-8},
		{"NoLACE", "nolace", 2e-7, 5e-8},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refOut, err := runOSCELACEForwardHelper(binPath, inputLen, tc.mode)
			if err != nil {
				t.Fatalf("libopus OSCE %s forward helper run failed: %v", tc.mode, err)
			}
			if len(refOut) != inputLen {
				t.Fatalf("libopus reference output: got %d samples, want %d", len(refOut), inputLen)
			}

			out := make([]float32, inputLen)
			switch tc.mode {
			case "lace":
				var state osceLACE.LACEState
				if err := state.SetModel(model); err != nil {
					t.Fatalf("LACEState.SetModel: %v", err)
				}
				if !state.Loaded() {
					t.Fatalf("LACEState.Loaded() == false after SetModel")
				}
				if err := state.Process(in, out, features, numbits, periods); err != nil {
					t.Fatalf("LACEState.Process: %v", err)
				}
			case "nolace":
				var state osceLACE.NoLACEState
				if err := state.SetModel(model); err != nil {
					t.Fatalf("NoLACEState.SetModel: %v", err)
				}
				if !state.Loaded() {
					t.Fatalf("NoLACEState.Loaded() == false after SetModel")
				}
				if err := state.Process(in, out, features, numbits, periods); err != nil {
					t.Fatalf("NoLACEState.Process: %v", err)
				}
			}

			// Sanity: neither side should be all-zero.
			if rmsOfFloat32(out) == 0 {
				t.Fatalf("gopus %s output has zero energy", tc.mode)
			}
			if rmsOfFloat32(refOut) == 0 {
				t.Fatalf("libopus %s reference has zero energy", tc.mode)
			}

			var maxAbsErr float32
			maxAbsIdx := -1
			var sumSq float64
			for i := 0; i < inputLen; i++ {
				if math.IsNaN(float64(out[i])) || math.IsInf(float64(out[i]), 0) {
					t.Fatalf("gopus %s output[%d]=%v is not finite", tc.mode, i, out[i])
				}
				if math.IsNaN(float64(refOut[i])) || math.IsInf(float64(refOut[i]), 0) {
					t.Fatalf("libopus %s reference[%d]=%v is not finite", tc.mode, i, refOut[i])
				}
				d := out[i] - refOut[i]
				ad := d
				if ad < 0 {
					ad = -ad
				}
				if ad > maxAbsErr {
					maxAbsErr = ad
					maxAbsIdx = i
				}
				sumSq += float64(d) * float64(d)
			}
			rms := math.Sqrt(sumSq / float64(inputLen))
			t.Logf("OSCE %s forward-pass parity: maxAbs=%g (idx %d) rms=%g (tolerances: maxAbs<=%g rms<=%g)",
				tc.name, maxAbsErr, maxAbsIdx, rms, tc.outputAbsTolerance, tc.outputRMSTolerance)
			if maxAbsErr > tc.outputAbsTolerance {
				t.Fatalf("OSCE %s forward-pass max-abs error %g exceeds %g (signal-net divergence beyond numerical contract)",
					tc.name, maxAbsErr, tc.outputAbsTolerance)
			}
			if rms > tc.outputRMSTolerance {
				t.Fatalf("OSCE %s forward-pass rms error %g exceeds %g (signal-net divergence beyond numerical contract)",
					tc.name, rms, tc.outputRMSTolerance)
			}
		})
	}
}

func TestOSCELACEForwardTraceLocatesFirstDivergence(t *testing.T) {
	if os.Getenv("GOPUS_TRACE_OSCE_LACE") != "1" {
		t.Skip("set GOPUS_TRACE_OSCE_LACE=1 to run the opt-in LACE stage trace")
	}
	libopustest.RequireOracle(t)

	binPath, err := getLibopusOSCELACEForwardHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE LACE forward", err)
	}

	blob := requireLibopusOSCELACEModelBlob(t)
	parsed, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	model, err := osceLACE.Load(parsed)
	if err != nil {
		t.Fatalf("osceLACE.Load: %v", err)
	}

	const inputLen = 320
	xq16 := make([]int16, inputLen)
	in := make([]float32, inputLen)
	for i := 0; i < inputLen; i++ {
		v := 0.5 * math.Sin(2*math.Pi*1000*float64(i)/16000)
		q := int(math.Round(v * 32767))
		if q > 32767 {
			q = 32767
		} else if q < -32768 {
			q = -32768
		}
		xq16[i] = int16(q)
		in[i] = float32(xq16[i]) / 32768
	}
	features := make([]float32, 4*93)
	numbits := []float32{0, 0}
	periods := []int{60, 60, 60, 60}

	refRecords, err := runOSCELACEForwardTraceHelper(binPath, inputLen, "lace")
	if err != nil {
		t.Fatalf("libopus OSCE LACE trace helper run failed: %v", err)
	}

	var state osceLACE.LACEState
	if err := state.SetModel(model); err != nil {
		t.Fatalf("LACEState.SetModel: %v", err)
	}
	out := make([]float32, inputLen)
	gotRecords, err := state.ProcessTrace(in, out, features, numbits, periods)
	if err != nil {
		t.Fatalf("LACEState.ProcessTrace: %v", err)
	}

	if len(gotRecords) != len(refRecords) {
		t.Fatalf("trace record count: got %d want %d", len(gotRecords), len(refRecords))
	}
	firstDivergence := ""
	for i := range gotRecords {
		got := gotRecords[i]
		ref := refRecords[i]
		if got.Stage != ref.Stage || got.Subframe != ref.Subframe ||
			got.Channels != ref.Channels || got.SamplesPerChannel != ref.SamplesPerChannel ||
			len(got.Values) != len(ref.Values) {
			t.Fatalf("trace record %d shape mismatch: got stage=%d subframe=%d channels=%d samples=%d len=%d; want stage=%d subframe=%d channels=%d samples=%d len=%d",
				i,
				got.Stage, got.Subframe, got.Channels, got.SamplesPerChannel, len(got.Values),
				ref.Stage, ref.Subframe, ref.Channels, ref.SamplesPerChannel, len(ref.Values))
		}
		maxAbs, maxIdx, rms := compareFloat32(got.Values, ref.Values)
		t.Logf("LACE trace %-22s maxAbs=%g idx=%d rms=%g", traceStageName(got.Stage), maxAbs, maxIdx, rms)
		if firstDivergence == "" && (maxAbs > 1e-5 || rms > 1e-6) {
			firstDivergence = traceStageName(got.Stage)
		}
	}
	if firstDivergence == "" {
		t.Log("LACE trace is within captured-stage parity thresholds")
	} else {
		t.Logf("first captured LACE divergence: %s", firstDivergence)
	}
}

func TestOSCENoLACEForwardTraceLocatesFirstDivergence(t *testing.T) {
	if os.Getenv("GOPUS_TRACE_OSCE_LACE") != "1" {
		t.Skip("set GOPUS_TRACE_OSCE_LACE=1 to run the opt-in NoLACE stage trace")
	}
	libopustest.RequireOracle(t)

	binPath, err := getLibopusOSCELACEForwardHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE LACE forward", err)
	}

	blob := requireLibopusOSCELACEModelBlob(t)
	parsed, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	model, err := osceLACE.Load(parsed)
	if err != nil {
		t.Fatalf("osceLACE.Load: %v", err)
	}

	const inputLen = 320
	in := make([]float32, inputLen)
	for i := 0; i < inputLen; i++ {
		v := 0.5 * math.Sin(2*math.Pi*1000*float64(i)/16000)
		q := int(math.Round(v * 32767))
		if q > 32767 {
			q = 32767
		} else if q < -32768 {
			q = -32768
		}
		in[i] = float32(int16(q)) / 32768
	}
	features := make([]float32, 4*93)
	numbits := []float32{0, 0}
	periods := []int{60, 60, 60, 60}

	refRecords, err := runOSCELACEForwardTraceHelper(binPath, inputLen, "nolace")
	if err != nil {
		t.Fatalf("libopus OSCE NoLACE trace helper run failed: %v", err)
	}

	var state osceLACE.NoLACEState
	if err := state.SetModel(model); err != nil {
		t.Fatalf("NoLACEState.SetModel: %v", err)
	}
	out := make([]float32, inputLen)
	gotRecords, err := state.ProcessTrace(in, out, features, numbits, periods)
	if err != nil {
		t.Fatalf("NoLACEState.ProcessTrace: %v", err)
	}

	if len(gotRecords) != len(refRecords) {
		t.Fatalf("trace record count: got %d want %d", len(gotRecords), len(refRecords))
	}
	firstDivergence := ""
	for i := range gotRecords {
		got := gotRecords[i]
		ref := refRecords[i]
		if got.Stage != ref.Stage || len(got.Values) != len(ref.Values) {
			t.Fatalf("trace record %d shape mismatch: got stage=%d len=%d; want stage=%d len=%d",
				i, got.Stage, len(got.Values), ref.Stage, len(ref.Values))
		}
		maxAbs, maxIdx, rms := compareFloat32(got.Values, ref.Values)
		t.Logf("NoLACE trace %-14s maxAbs=%g idx=%d rms=%g", traceStageName(got.Stage), maxAbs, maxIdx, rms)
		if firstDivergence == "" && (maxAbs > 1e-6 || rms > 1e-7) {
			firstDivergence = traceStageName(got.Stage)
		}
	}
	if firstDivergence == "" {
		t.Log("NoLACE trace is within captured-stage parity thresholds")
	} else {
		t.Logf("first captured NoLACE divergence: %s", firstDivergence)
	}
}

var libopusOSCELACEForwardHelper libopustest.HelperCache

// getLibopusOSCELACEForwardHelperPath lazily builds (against the OSCE-enabled
// libopus build) the C reference helper `libopus_osce_lace_forward.c` and
// caches the binary path for the lifetime of the test process.
func getLibopusOSCELACEForwardHelperPath() (string, error) {
	return cachedLibopusOSCEHelperPath(&libopusOSCELACEForwardHelper, "libopus_osce_lace_forward.c", "gopus_libopus_osce_lace_forward", true)
}

// runOSCELACEForwardHelper invokes the libopus OSCE LACE/NoLACE forward
// helper for `numIn16` samples (must be 320) in the requested mode ("lace"
// or "nolace"), parses the binary payload, and returns the libopus 16 kHz
// float output.
func runOSCELACEForwardHelper(binPath string, numIn16 int, mode string) (out16k []float32, err error) {
	payload, err := libopustest.RunHelperArgsEnv(binPath, nil, []string{fmt.Sprintf("MODE=%s", mode)}, fmt.Sprintf("%d", numIn16))
	if err != nil {
		return nil, fmt.Errorf("run libopus OSCE LACE forward helper: %w", err)
	}
	reader, version, err := libopustest.NewOracleReaderMagicVersion("OSCE LACE forward", "OSCELAC\x00", payload)
	if err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("libopus OSCE LACE forward version=%d, want 1", version)
	}
	modeID := int(reader.I32())
	wantModeID := 0
	if mode == "nolace" {
		wantModeID = 1
	}
	if modeID != wantModeID {
		return nil, fmt.Errorf("libopus OSCE LACE forward output: mode_id=%d, want %d for mode %q", modeID, wantModeID, mode)
	}
	numOut := int(reader.I32())
	if numOut != numIn16 {
		return nil, fmt.Errorf("libopus OSCE LACE forward output: num_out=%d != num_in=%d", numOut, numIn16)
	}

	outBytes := numOut * 4
	reader.ExpectRemaining(outBytes)
	out16k = make([]float32, numOut)
	for i := range out16k {
		out16k[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out16k, nil
}

func runOSCELACEForwardTraceHelper(binPath string, numIn16 int, mode string) ([]osceLACE.TraceRecord, error) {
	payload, err := libopustest.RunHelperArgsEnv(binPath, nil, []string{fmt.Sprintf("MODE=%s", mode), "TRACE=1"}, fmt.Sprintf("%d", numIn16))
	if err != nil {
		return nil, fmt.Errorf("run libopus OSCE LACE trace helper: %w", err)
	}
	reader, version, err := libopustest.NewOracleReaderMagicVersion("OSCE LACE trace", "OSCELTR\x00", payload)
	if err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("libopus OSCE LACE trace version=%d, want 1", version)
	}
	modeID := int(reader.I32())
	wantModeID := 0
	if mode == "nolace" {
		wantModeID = 1
	}
	if modeID != wantModeID {
		return nil, fmt.Errorf("libopus OSCE LACE trace mode_id=%d, want %d", modeID, wantModeID)
	}
	sampleRate := int(reader.I32())
	if sampleRate != 16000 {
		return nil, fmt.Errorf("libopus OSCE LACE trace sample_rate=%d, want 16000", sampleRate)
	}
	frameSamples := int(reader.I32())
	if frameSamples != numIn16 {
		return nil, fmt.Errorf("libopus OSCE LACE trace frame_samples=%d, want %d", frameSamples, numIn16)
	}
	subframes := int(reader.I32())
	if subframes != 4 {
		return nil, fmt.Errorf("libopus OSCE LACE trace subframes=%d, want 4", subframes)
	}
	stageCount := int(reader.I32())
	if stageCount < 0 {
		return nil, fmt.Errorf("libopus OSCE LACE trace invalid stage_count=%d", stageCount)
	}

	records := make([]osceLACE.TraceRecord, 0, stageCount)
	for rec := 0; rec < stageCount; rec++ {
		stage := osceLACE.TraceStage(int(reader.I32()))
		subframe := int(reader.I32())
		channels := int(reader.I32())
		samplesPerChannel := int(reader.I32())
		valuesLen := int(reader.I32())
		if valuesLen < 0 {
			return nil, fmt.Errorf("libopus OSCE LACE trace record %d invalid values_len=%d", rec, valuesLen)
		}
		values := make([]float32, valuesLen)
		for i := range values {
			values[i] = reader.Float32()
		}
		if err := reader.Err(); err != nil {
			return nil, fmt.Errorf("libopus OSCE LACE trace record %d: %w", rec, err)
		}
		records = append(records, osceLACE.TraceRecord{
			Stage:             stage,
			Subframe:          subframe,
			Channels:          channels,
			SamplesPerChannel: samplesPerChannel,
			Values:            values,
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return records, nil
}

func compareFloat32(got, want []float32) (maxAbs float32, maxIdx int, rms float64) {
	maxIdx = -1
	var sumSq float64
	for i := range got {
		g := got[i]
		w := want[i]
		if math.IsNaN(float64(g)) && math.IsNaN(float64(w)) {
			continue
		}
		if math.IsInf(float64(g), 0) || math.IsInf(float64(w), 0) {
			if math.IsInf(float64(g), 1) && math.IsInf(float64(w), 1) {
				continue
			}
			if math.IsInf(float64(g), -1) && math.IsInf(float64(w), -1) {
				continue
			}
			return float32(math.Inf(1)), i, math.Inf(1)
		}
		d := g - w
		ad := d
		if ad < 0 {
			ad = -ad
		}
		if ad > maxAbs {
			maxAbs = ad
			maxIdx = i
		}
		sumSq += float64(d) * float64(d)
	}
	if len(got) != 0 {
		rms = math.Sqrt(sumSq / float64(len(got)))
	}
	return maxAbs, maxIdx, rms
}

func traceStageName(stage osceLACE.TraceStage) string {
	switch stage {
	case osceLACE.TraceStageInput:
		return "input"
	case osceLACE.TraceStageFeatures:
		return "features"
	case osceLACE.TraceStageNumbits:
		return "numbits"
	case osceLACE.TraceStagePeriods:
		return "periods"
	case osceLACE.TraceStagePreemph:
		return "preemph"
	case osceLACE.TraceStageFeatureNetConv1:
		return "feature_net_conv1"
	case osceLACE.TraceStageFeatureNetConv2Input:
		return "feature_net_conv2_input"
	case osceLACE.TraceStageFeatureNetConv2Linear:
		return "feature_net_conv2_linear"
	case osceLACE.TraceStageFeatureNetConv2:
		return "feature_net_conv2"
	case osceLACE.TraceStageFeatureNetTConv:
		return "feature_net_tconv"
	case osceLACE.TraceStageFeatureNetLatent:
		return "feature_net_latent"
	case osceLACE.TraceStagePostCF1:
		return "post_cf1"
	case osceLACE.TraceStagePostCF2:
		return "post_cf2"
	case osceLACE.TraceStagePostAF1:
		return "post_af1"
	case osceLACE.TraceStageDeemph:
		return "deemph"
	case osceLACE.TraceStageCF1KernelRaw:
		return "cf1_kernel_raw"
	case osceLACE.TraceStageCF1GainsRaw:
		return "cf1_gains_raw"
	case osceLACE.TraceStageCF1KernelScaled:
		return "cf1_kernel_scaled"
	case osceLACE.TraceStageCF1GainsScaled:
		return "cf1_gains_scaled"
	case osceLACE.TraceStageNLPreemph:
		return "nl_preemph"
	case osceLACE.TraceStageNLLatent:
		return "nl_latent"
	case osceLACE.TraceStageNLPostCF1:
		return "nl_post_cf1"
	case osceLACE.TraceStageNLPostCF2:
		return "nl_post_cf2"
	case osceLACE.TraceStageNLPostAF1:
		return "nl_post_af1"
	case osceLACE.TraceStageNLTDShape1:
		return "nl_tdshape1"
	case osceLACE.TraceStageNLPostAF2:
		return "nl_post_af2"
	case osceLACE.TraceStageNLTDShape2:
		return "nl_tdshape2"
	case osceLACE.TraceStageNLPostAF3:
		return "nl_post_af3"
	case osceLACE.TraceStageNLTDShape3:
		return "nl_tdshape3"
	case osceLACE.TraceStageNLPostAF4:
		return "nl_post_af4"
	case osceLACE.TraceStageNLDeemph:
		return "nl_deemph"
	default:
		return fmt.Sprintf("stage_%d", stage)
	}
}
