//go:build gopus_osce

package gopus

import (
	"encoding/binary"
	"fmt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dnnmath"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
	"math"
	"math/rand"
	"os"
	"runtime"
	"testing"
)

func TestOSCEBWEForwardPassMatchesLibopusNumericalParity(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := getLibopusOSCEBWEForwardHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE BWE forward", err)
	}

	blob := requireLibopusOSCEBWEModelBlob(t)
	parsed, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	if !parsed.SupportsOSCEBWE() {
		t.Fatalf("parsed OSCE BWE blob does not advertise SupportsOSCEBWE()")
	}

	cases := []struct {
		name               string
		numIn16            int
		numFrames          int
		outputAbsTolerance float32
		outputRMSTolerance float64
	}{
		{"10ms", 160, 1, 0, 0},
		{"20ms", 320, 2, 0, 0},
	}

	const (
		featureTolerance   = 6e-7
		instafreqTolerance = 1.5e-7
	)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refFeatures, refOut, err := runOSCEBWEForwardHelper(binPath, tc.numIn16)
			if err != nil {
				t.Fatalf("libopus OSCE BWE forward helper run failed: %v", err)
			}
			if len(refFeatures) != tc.numFrames*osceBWE.FeatureDim {
				t.Fatalf("libopus reference features: got %d floats, want %d", len(refFeatures), tc.numFrames*osceBWE.FeatureDim)
			}
			if len(refOut) != 3*tc.numIn16 {
				t.Fatalf("libopus reference output: got %d samples, want %d", len(refOut), 3*tc.numIn16)
			}

			// Generate the same 1 kHz sinusoid (matching the C helper).
			xq16 := make([]int16, tc.numIn16)
			in16f := make([]float32, tc.numIn16)
			for i := 0; i < tc.numIn16; i++ {
				v := 0.5 * math.Sin(2*math.Pi*1000*float64(i)/16000)
				q := int(math.Round(v * 32767))
				if q > 32767 {
					q = 32767
				} else if q < -32768 {
					q = -32768
				}
				xq16[i] = int16(q)
				in16f[i] = float32(xq16[i]) / 32768
			}

			// Compute features via the gopus port.
			var feat osceBWE.FeatureState
			feat.Reset()
			gopusFeatures := make([]float32, tc.numFrames*osceBWE.FeatureDim)
			feat.CalculateFeatures(gopusFeatures, xq16)

			// Compare feature bits, then track maximum absolute error in the
			// lmspec block (first 32 floats of each 114-vector) and the
			// instafreq block (remaining 82) for diagnostics.
			maxFeatErrLM := float32(0)
			maxIdxLM := -1
			maxFeatErrIF := float32(0)
			maxIdxIF := -1
			for i := range gopusFeatures {
				if math.Float32bits(gopusFeatures[i]) != math.Float32bits(refFeatures[i]) {
					t.Fatalf("feature=%d Go=%08x C=%08x", i, math.Float32bits(gopusFeatures[i]), math.Float32bits(refFeatures[i]))
				}
				d := gopusFeatures[i] - refFeatures[i]
				if d < 0 {
					d = -d
				}
				within := i % osceBWE.FeatureDim
				if within < 32 {
					if d > maxFeatErrLM {
						maxFeatErrLM = d
						maxIdxLM = i
					}
				} else {
					if d > maxFeatErrIF {
						maxFeatErrIF = d
						maxIdxIF = i
					}
				}
			}
			t.Logf("feature-extractor lmspec maxAbs=%g (idx %d), instafreq maxAbs=%g (idx %d)",
				maxFeatErrLM, maxIdxLM, maxFeatErrIF, maxIdxIF)
			if maxFeatErrLM > featureTolerance {
				t.Logf("first 8 libopus lmspec: %v", refFeatures[:8])
				t.Logf("first 8 gopus  lmspec: %v", gopusFeatures[:8])
				t.Errorf("OSCE BWE feature extractor lmspec drifted from libopus beyond tolerance")
			}
			if maxFeatErrIF > instafreqTolerance {
				t.Errorf("OSCE BWE feature extractor instafreq drift %g exceeds %g", maxFeatErrIF, instafreqTolerance)
			}

			// Drive the gopus forward pass with the *libopus* features so
			// we are measuring strictly the signal-net divergence, not
			// compounded feature-extractor + signal-net error.
			var state osceBWE.State
			if err := state.SetModel(parsed); err != nil {
				t.Fatalf("state.SetModel: %v", err)
			}
			if !state.Loaded() {
				t.Fatalf("state.Loaded() == false after SetModel")
			}

			gopusOut := make([]float32, 3*tc.numIn16)
			if err := state.ProcessDelayed(in16f, gopusOut, refFeatures); err != nil {
				t.Fatalf("state.ProcessDelayed: %v", err)
			}

			var maxAbsErr float32
			var sumSq float64
			for i := 0; i < len(refOut); i++ {
				if math.Float32bits(gopusOut[i]) != math.Float32bits(refOut[i]) {
					t.Fatalf("delayed sample=%d Go=%08x C=%08x", i, math.Float32bits(gopusOut[i]), math.Float32bits(refOut[i]))
				}
				d := gopusOut[i] - refOut[i]
				ad := d
				if ad < 0 {
					ad = -ad
				}
				if ad > maxAbsErr {
					maxAbsErr = ad
				}
				sumSq += float64(d) * float64(d)
			}
			rms := math.Sqrt(sumSq / float64(len(refOut)))
			t.Logf("OSCE BWE forward-pass parity (%s): maxAbs=%g rms=%g (tolerances: maxAbs<=%g rms<=%g)",
				tc.name, maxAbsErr, rms, tc.outputAbsTolerance, tc.outputRMSTolerance)

			// Sanity: neither side should be all-zero.
			if rmsOfFloat32(gopusOut) == 0 {
				t.Fatalf("gopus output has zero energy")
			}
			if rmsOfFloat32(refOut) == 0 {
				t.Fatalf("libopus reference has zero energy")
			}

			if maxAbsErr > tc.outputAbsTolerance {
				t.Errorf("OSCE BWE forward-pass max-abs error %g exceeds %g (signal-net divergence beyond numerical contract)", maxAbsErr, tc.outputAbsTolerance)
			}
			if rms > tc.outputRMSTolerance {
				t.Errorf("OSCE BWE forward-pass rms error %g exceeds %g (signal-net divergence beyond numerical contract)", rms, tc.outputRMSTolerance)
			}
		})
	}
}

func TestOSCEBWERawSignalNetMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := getLibopusOSCEBWEForwardHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE BWE forward", err)
	}

	blob := requireLibopusOSCEBWEModelBlob(t)
	parsed, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	if !parsed.SupportsOSCEBWE() {
		t.Fatalf("parsed OSCE BWE blob does not advertise SupportsOSCEBWE()")
	}

	cases := []struct {
		name    string
		numIn16 int
		mode    string
	}{
		{"10ms", 160, "raw"},
		{"20ms", 320, "raw"},
		{"10ms_consecutive", 160, "raw-consecutive"},
	}

	const (
		rawAbsTolerance = 2.5e-7
		rawRMSTolerance = 6e-8
	)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refFeatures, refOut, err := runOSCEBWEForwardHelperMode(binPath, tc.numIn16, tc.mode)
			if err != nil {
				t.Fatalf("libopus OSCE BWE raw helper failed: %v", err)
			}
			if len(refOut) != 3*tc.numIn16 {
				t.Fatalf("libopus raw output: got %d samples, want %d", len(refOut), 3*tc.numIn16)
			}

			in16f := make([]float32, tc.numIn16)
			for i := 0; i < tc.numIn16; i++ {
				v := 0.5 * math.Sin(2*math.Pi*1000*float64(i)/16000)
				q := int(math.Round(v * 32767))
				if q > 32767 {
					q = 32767
				} else if q < -32768 {
					q = -32768
				}
				in16f[i] = float32(int16(q)) / 32768
			}

			var state osceBWE.State
			if err := state.SetModel(parsed); err != nil {
				t.Fatalf("state.SetModel: %v", err)
			}
			if tc.mode == "raw-consecutive" {
				firstFeatures, _, err := runOSCEBWEForwardHelperMode(binPath, tc.numIn16, "raw")
				if err != nil {
					t.Fatalf("libopus OSCE BWE first-frame raw helper failed: %v", err)
				}
				scratch := make([]float32, 3*tc.numIn16)
				if err := state.Process(in16f, scratch, firstFeatures); err != nil {
					t.Fatalf("state.Process first frame: %v", err)
				}
			}

			gotOut := make([]float32, 3*tc.numIn16)
			if err := state.Process(in16f, gotOut, refFeatures); err != nil {
				t.Fatalf("state.Process: %v", err)
			}

			var maxAbsErr float32
			var sumSq float64
			for i := range refOut {
				if math.Float32bits(gotOut[i]) != math.Float32bits(refOut[i]) {
					t.Fatalf("raw sample=%d Go=%08x C=%08x", i, math.Float32bits(gotOut[i]), math.Float32bits(refOut[i]))
				}
				d := gotOut[i] - refOut[i]
				ad := d
				if ad < 0 {
					ad = -ad
				}
				if ad > maxAbsErr {
					maxAbsErr = ad
				}
				sumSq += float64(d) * float64(d)
			}
			if allocs := testing.AllocsPerRun(20, func() {
				if err := state.Process(in16f, gotOut, refFeatures); err != nil {
					t.Fatal(err)
				}
			}); allocs != 0 {
				t.Fatalf("warm signal-net allocations=%g, want 0", allocs)
			}
			rms := math.Sqrt(sumSq / float64(len(refOut)))
			t.Logf("OSCE BWE raw signal-net parity (%s): maxAbs=%g rms=%g (tolerances: maxAbs<=%g rms<=%g)",
				tc.name, maxAbsErr, rms, rawAbsTolerance, rawRMSTolerance)
			if maxAbsErr > rawAbsTolerance {
				t.Errorf("OSCE BWE raw signal-net max-abs error %g exceeds %g", maxAbsErr, rawAbsTolerance)
			}
			if rms > rawRMSTolerance {
				t.Errorf("OSCE BWE raw signal-net rms error %g exceeds %g", rms, rawRMSTolerance)
			}
		})
	}
}

// TestOSCEBWEForwardPassPLCContinuityMatchesLibopus drives the BWE forward
// pass twice in succession on the same 16 kHz lowband (the canonical state-
// continuity scenario the PLC path exercises: a good SILK WB frame is
// followed by a concealed SILK WB frame and both invoke the same per-channel
// `osce_bwe` state). The second-frame output is the one the listener hears
// during PLC. The test compares its feature and output bits with the matching
// libopus helper; the error measurements are diagnostics.
func TestOSCEBWEForwardPassPLCContinuityMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := getLibopusOSCEBWEForwardHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE BWE forward", err)
	}

	blob := requireLibopusOSCEBWEModelBlob(t)
	parsed, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	if !parsed.SupportsOSCEBWE() {
		t.Fatalf("parsed OSCE BWE blob does not advertise SupportsOSCEBWE()")
	}

	const (
		numIn16            = 160 // 10 ms @ 16 kHz: minimum BWE frame
		numFrames          = 1
		featureTolerance   = 6e-7
		instafreqTolerance = 1.5e-7
		// Wrapper parity is int16-domain: one LSB is 1/32768.
		outputAbsTolerance = 3.2e-5
		outputRMSTolerance = 1.6e-6
	)

	refFeatures, refOut, err := runOSCEBWEForwardHelperMode(binPath, numIn16, "consecutive")
	if err != nil {
		t.Fatalf("libopus OSCE BWE consecutive helper failed: %v", err)
	}
	if len(refFeatures) != numFrames*osceBWE.FeatureDim {
		t.Fatalf("libopus consecutive features: got %d floats, want %d", len(refFeatures), numFrames*osceBWE.FeatureDim)
	}
	if len(refOut) != 3*numIn16 {
		t.Fatalf("libopus consecutive output: got %d samples, want %d", len(refOut), 3*numIn16)
	}

	// Generate the same 1 kHz sinusoid (matches the C helper).
	xq16 := make([]int16, numIn16)
	in16f := make([]float32, numIn16)
	for i := 0; i < numIn16; i++ {
		v := 0.5 * math.Sin(2*math.Pi*1000*float64(i)/16000)
		q := int(math.Round(v * 32767))
		if q > 32767 {
			q = 32767
		} else if q < -32768 {
			q = -32768
		}
		xq16[i] = int16(q)
		in16f[i] = float32(xq16[i]) / 32768
	}

	// Drive a single feature/forward state through two consecutive 10 ms
	// frames so the second-frame state mirrors the libopus side. The PLC
	// path in decoder_osce_bwe_apply.go does exactly this via the
	// per-channel osceBWEFeatures + osceBWERuntime state.
	var feat osceBWE.FeatureState
	feat.Reset()
	var state osceBWE.State
	if err := state.SetModel(parsed); err != nil {
		t.Fatalf("state.SetModel: %v", err)
	}

	gopusFeatures1 := make([]float32, numFrames*osceBWE.FeatureDim)
	feat.CalculateFeatures(gopusFeatures1, xq16)
	gopusOut1 := make([]float32, 3*numIn16)
	if err := state.Process(in16f, gopusOut1, gopusFeatures1); err != nil {
		t.Fatalf("state.Process (frame 1): %v", err)
	}

	// Second frame: same input but the state has now consumed the first
	// frame so the signal_history / last_spec / GRU hidden state differ.
	gopusFeatures2 := make([]float32, numFrames*osceBWE.FeatureDim)
	feat.CalculateFeatures(gopusFeatures2, xq16)
	gopusOut2 := make([]float32, 3*numIn16)
	if err := state.Process(in16f, gopusOut2, gopusFeatures2); err != nil {
		t.Fatalf("state.Process (frame 2): %v", err)
	}

	// Features: feed gopus second-frame output the libopus second-frame
	// features so feature-extractor drift is isolated from signal-net drift.
	maxFeatErrLM := float32(0)
	maxFeatErrIF := float32(0)
	for i := range gopusFeatures2 {
		if math.Float32bits(gopusFeatures2[i]) != math.Float32bits(refFeatures[i]) {
			t.Fatalf("continuity feature=%d Go=%08x C=%08x", i, math.Float32bits(gopusFeatures2[i]), math.Float32bits(refFeatures[i]))
		}
		d := gopusFeatures2[i] - refFeatures[i]
		if d < 0 {
			d = -d
		}
		within := i % osceBWE.FeatureDim
		if within < 32 {
			if d > maxFeatErrLM {
				maxFeatErrLM = d
			}
		} else {
			if d > maxFeatErrIF {
				maxFeatErrIF = d
			}
		}
	}
	t.Logf("PLC continuity feature-extractor lmspec maxAbs=%g, instafreq maxAbs=%g",
		maxFeatErrLM, maxFeatErrIF)
	if maxFeatErrLM > featureTolerance {
		t.Errorf("PLC continuity feature extractor lmspec drift %g exceeds %g", maxFeatErrLM, featureTolerance)
	}
	if maxFeatErrIF > instafreqTolerance {
		t.Errorf("PLC continuity feature extractor instafreq drift %g exceeds %g", maxFeatErrIF, instafreqTolerance)
	}

	// Re-run the gopus second frame against the libopus features so we are
	// strictly measuring the signal-net divergence. This mirrors the
	// forward-pass test methodology.
	feat.Reset()
	var state2 osceBWE.State
	if err := state2.SetModel(parsed); err != nil {
		t.Fatalf("state2.SetModel: %v", err)
	}
	// First frame: libopus features (snapshot from a fresh feature state).
	refFeatures1, _, err := runOSCEBWEForwardHelperMode(binPath, numIn16, "")
	if err != nil {
		t.Fatalf("libopus OSCE BWE forward helper failed: %v", err)
	}
	scratch := make([]float32, 3*numIn16)
	if err := state2.ProcessDelayed(in16f, scratch, refFeatures1); err != nil {
		t.Fatalf("state2.ProcessDelayed (frame 1, libopus feats): %v", err)
	}
	gopusOut2WithLibopusFeat := make([]float32, 3*numIn16)
	if err := state2.ProcessDelayed(in16f, gopusOut2WithLibopusFeat, refFeatures); err != nil {
		t.Fatalf("state2.ProcessDelayed (frame 2, libopus feats): %v", err)
	}

	var maxAbsErr float32
	var sumSq float64
	for i := 0; i < len(refOut); i++ {
		if math.Float32bits(gopusOut2WithLibopusFeat[i]) != math.Float32bits(refOut[i]) {
			t.Fatalf("continuity sample=%d Go=%08x C=%08x", i, math.Float32bits(gopusOut2WithLibopusFeat[i]), math.Float32bits(refOut[i]))
		}
		d := gopusOut2WithLibopusFeat[i] - refOut[i]
		ad := d
		if ad < 0 {
			ad = -ad
		}
		if ad > maxAbsErr {
			maxAbsErr = ad
		}
		sumSq += float64(d) * float64(d)
	}
	rms := math.Sqrt(sumSq / float64(len(refOut)))
	t.Logf("OSCE BWE PLC continuity parity (frame 2 of 2): maxAbs=%g rms=%g (tolerances: maxAbs<=%g rms<=%g)",
		maxAbsErr, rms, outputAbsTolerance, outputRMSTolerance)

	if rmsOfFloat32(gopusOut2WithLibopusFeat) == 0 {
		t.Fatalf("gopus second-frame output has zero energy")
	}
	if rmsOfFloat32(refOut) == 0 {
		t.Fatalf("libopus second-frame reference has zero energy")
	}
	if maxAbsErr > outputAbsTolerance {
		t.Errorf("OSCE BWE PLC continuity max-abs error %g exceeds %g", maxAbsErr, outputAbsTolerance)
	}
	if rms > outputRMSTolerance {
		t.Errorf("OSCE BWE PLC continuity rms error %g exceeds %g", rms, outputRMSTolerance)
	}
}

// TestOSCEBWECrossFade10msMatchesLibopus drives the SILK WB -> Hybrid SWB
// fade-out cross-fade directly. The gopus port of osce_bwe_cross_fade_10ms
// operates on float32 PCM in [-1, 1] but mirrors libopus' int16-domain
// writeback, so the normalised outputs should match exactly.
func TestOSCEBWECrossFade10msMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := getLibopusOSCEBWEForwardHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE BWE forward", err)
	}

	const numIn16 = 160
	_, refOut, err := runOSCEBWEForwardHelperMode(binPath, numIn16, "crossfade")
	if err != nil {
		t.Fatalf("libopus OSCE BWE crossfade helper failed: %v", err)
	}
	if len(refOut) != 480 {
		t.Fatalf("libopus crossfade output: got %d samples, want 480", len(refOut))
	}

	// Reproduce the same fade-in / fade-out ramps the C helper generates.
	fadeinF := make([]float32, 480)
	fadeoutF := make([]float32, 480)
	for i := 0; i < 480; i++ {
		fi := int32((i*24000)/480) - 12000
		fo := int32(12000 - ((i * 24000) / 480))
		if fi > 32767 {
			fi = 32767
		} else if fi < -32768 {
			fi = -32768
		}
		if fo > 32767 {
			fo = 32767
		} else if fo < -32768 {
			fo = -32768
		}
		fadeinF[i] = float32(fi) / 32768
		fadeoutF[i] = float32(fo) / 32768
	}

	osceBWECrossFade10ms(fadeinF, fadeoutF, 480)

	for i := 0; i < 480; i++ {
		if math.IsNaN(float64(fadeinF[i])) || math.IsInf(float64(fadeinF[i]), 0) {
			t.Fatalf("gopus crossfade output[%d]=%v is not finite", i, fadeinF[i])
		}
		if math.IsNaN(float64(refOut[i])) || math.IsInf(float64(refOut[i]), 0) {
			t.Fatalf("libopus crossfade output[%d]=%v is not finite", i, refOut[i])
		}
		if gotBits, wantBits := math.Float32bits(fadeinF[i]), math.Float32bits(refOut[i]); gotBits != wantBits {
			t.Fatalf("OSCE BWE crossfade first difference at sample %d: Go=%08x libopus=%08x", i, gotBits, wantBits)
		}
	}
}

func rmsOfFloat32(x []float32) float64 {
	if len(x) == 0 {
		return 0
	}
	var s float64
	for _, v := range x {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s / float64(len(x)))
}

var libopusOSCEBWEForwardHelper libopustest.HelperCache

// getLibopusOSCEBWEForwardHelperPath lazily builds (against the OSCE-enabled
// libopus build) the C reference helper `libopus_osce_bwe_forward.c` and
// caches the binary path for the lifetime of the test process.
func getLibopusOSCEBWEForwardHelperPath() (string, error) {
	return cachedLibopusOSCEHelperPath(&libopusOSCEBWEForwardHelper, "libopus_osce_bwe_forward.c", "gopus_libopus_osce_bwe_forward", true)
}

// runOSCEBWEForwardHelper invokes the libopus OSCE BWE forward helper with
// the given input length (160 or 320), parses the binary payload, and
// returns the libopus feature vectors and the libopus 48 kHz output (float
// in [-1, 1], obtained by dividing libopus's int16 PCM by 32768).
func runOSCEBWEForwardHelper(binPath string, numIn16 int) (features, out48k []float32, err error) {
	return runOSCEBWEForwardHelperMode(binPath, numIn16, "")
}

// runOSCEBWEForwardHelperMode invokes the libopus OSCE BWE helper with an
// explicit mode argument. Recognised modes are:
//   - "" / "forward" : single osce_bwe pass on a 1 kHz sinusoid (default).
//   - "consecutive"  : two back-to-back osce_bwe passes, the second-frame
//     output is emitted (covers PLC frame-to-frame state continuity).
//   - "crossfade"    : runs osce_bwe_cross_fade_10ms directly on two
//     deterministic ramps; emits the 480-sample crossfaded result.
func runOSCEBWEForwardHelperMode(binPath string, numIn16 int, mode string) (features, out48k []float32, err error) {
	args := []string{fmt.Sprintf("%d", numIn16)}
	if mode != "" {
		args = append(args, mode)
	}
	payload, err := libopustest.RunHelperArgs(binPath, nil, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("run libopus OSCE BWE forward helper: %w", err)
	}
	reader, version, err := libopustest.NewOracleReaderMagicVersion("OSCE BWE forward", "OSCEBWE\x00", payload)
	if err != nil {
		return nil, nil, err
	}
	if version != 1 {
		return nil, nil, fmt.Errorf("libopus OSCE BWE forward version=%d, want 1", version)
	}
	numFrames := int(reader.I32())
	_ = int(reader.I32()) // num_subframes (not used directly here)
	numOut := int(reader.I32())

	featBytes := numFrames * osceBWE.FeatureDim * 4
	outBytes := numOut * 4
	reader.ExpectRemaining(featBytes + outBytes)
	features = make([]float32, numFrames*osceBWE.FeatureDim)
	for i := range features {
		features[i] = reader.Float32()
	}
	out48k = make([]float32, numOut)
	for i := range out48k {
		out48k[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	return features, out48k, nil
}

func TestOSCEBWEModelForwardPassMatchesLibopus(t *testing.T) {
	blob := requireLibopusOSCEBWEModelBlob(t)

	parsed, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	if !parsed.SupportsOSCEBWE() {
		t.Fatalf("parsed BWE blob does not satisfy SupportsOSCEBWE()")
	}

	var state osceBWE.State
	if err := state.SetModel(parsed); err != nil {
		t.Fatalf("state.SetModel: %v", err)
	}
	if !state.Loaded() {
		t.Fatalf("state.Loaded() == false after SetModel")
	}

	cases := []struct {
		name        string
		inputLen    int
		numFrames   int
		expectedOut int
	}{
		{name: "10ms", inputLen: 160, numFrames: 1, expectedOut: 480},
		{name: "20ms", inputLen: 320, numFrames: 2, expectedOut: 960},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset between subtests so previous-frame state does not leak.
			state.Reset()

			in := make([]float32, tc.inputLen)
			// Generate a 1 kHz sinusoid at 16 kHz (well within the SILK
			// lowband). Amplitude 0.5 keeps it inside [-1, 1].
			for i := 0; i < tc.inputLen; i++ {
				in[i] = float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/16000))
			}

			// Zero features -- exercises every layer without depending on a
			// not-yet-implemented feature extractor.
			features := make([]float32, tc.numFrames*osceBWE.FeatureDim)

			out := make([]float32, 3*tc.inputLen)
			if err := state.Process(in, out, features); err != nil {
				t.Fatalf("state.Process: %v", err)
			}

			if len(out) != tc.expectedOut {
				t.Fatalf("output length: got %d, want %d", len(out), tc.expectedOut)
			}

			var energy float64
			for _, v := range out {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					t.Fatalf("output contains NaN/Inf: %v", v)
				}
				if v < -2 || v > 2 {
					t.Fatalf("output out of expected float32 PCM range [-2,2]: %v", v)
				}
				energy += float64(v) * float64(v)
			}
			if energy == 0 {
				t.Fatalf("output energy is zero -- forward pass produced silence for a non-zero input")
			}
		})
	}
}

// TestOSCEBWEModelForwardPassRejectsBadInputs verifies the Process API
// rejects inputs that don't match libopus' restricted 10/20 ms framing.
func TestOSCEBWEModelForwardPassRejectsBadInputs(t *testing.T) {
	blob := requireLibopusOSCEBWEModelBlob(t)
	parsed, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	var state osceBWE.State
	if err := state.SetModel(parsed); err != nil {
		t.Fatalf("state.SetModel: %v", err)
	}

	// Wrong input length.
	in := make([]float32, 200)
	out := make([]float32, 3*200)
	feats := make([]float32, osceBWE.FeatureDim)
	if err := state.Process(in, out, feats); err == nil {
		t.Fatalf("Process accepted input of length 200; expected error")
	}

	// Too-short output buffer.
	in = make([]float32, 160)
	out = make([]float32, 100)
	feats = make([]float32, osceBWE.FeatureDim)
	if err := state.Process(in, out, feats); err == nil {
		t.Fatalf("Process accepted truncated output buffer; expected error")
	}

	// Wrong features length.
	in = make([]float32, 160)
	out = make([]float32, 480)
	feats = make([]float32, osceBWE.FeatureDim-1)
	if err := state.Process(in, out, feats); err == nil {
		t.Fatalf("Process accepted features of wrong length; expected error")
	}
}

func TestOSCEBWEInt8LibopusKernelParity(t *testing.T) {
	bweBlob := requireLibopusOSCEBWEModelBlob(t)
	parsed, err := dnnblob.Clone(bweBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	model, err := osceBWE.LoadModel(parsed)
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}

	// Each entry pairs a libopus int8-capable layer with a pointer to the
	// loaded LinearLayer struct.
	cases := []struct {
		name  string
		layer *osceBWE.LinearLayer
	}{
		{"fnet_conv2", &model.FNetConv2},
		{"fnet_gru_input", &model.FNetGRUInput},
		{"fnet_gru_recurrent", &model.FNetGRURecurrent},
		{"fnet_tconv", &model.FNetTConv},
		{"tdshape1_alpha1_f", &model.TDShape1Alpha1F},
		{"tdshape2_alpha1_f", &model.TDShape2Alpha1F},
		{"af1_kernel", &model.AF1Kernel},
		{"af2_kernel", &model.AF2Kernel},
		{"af3_kernel", &model.AF3Kernel},
	}

	rng := rand.New(rand.NewSource(0xBBE0_1A8))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.layer.Weights.Empty() {
				t.Skipf("layer %s has no int8 weights in blob", tc.name)
			}
			m := tc.layer.NbInputs
			n := tc.layer.NbOutputs

			// Input bounded to (-1, 1) to stay within the int8
			// quantisation range that libopus expects after the
			// tanh-bounded activations feeding most BBWENet linear
			// layers.
			in := make([]float32, m)
			for i := range in {
				in[i] = float32(math.Tanh(rng.Float64()*4 - 2))
			}

			// Kernel output.
			got := make([]float32, n)
			osceBWE.EvaluateLayerInt8(tc.layer, got, in)

			// Reference: dequantise the libopus int8 weight tiles
			// into a column-major float matrix (with per-row scale
			// baked in) and run a plain sgemv. This is the
			// "expected behaviour" of cgemv8x4 expressed as the
			// equivalent float computation, so any divergence
			// indicates the kernel reads the libopus tile layout
			// incorrectly.
			ref := referenceCGEMV8x4OnLibopusLayer(tc.layer, in)

			var maxDiff, maxAbs float32
			for i := 0; i < n; i++ {
				d := got[i] - ref[i]
				if d < 0 {
					d = -d
				}
				if d > maxDiff {
					maxDiff = d
				}
				v := ref[i]
				if v < 0 {
					v = -v
				}
				if v > maxAbs {
					maxAbs = v
				}
			}
			// Allow a single ULP of relative slack to absorb the
			// int32->float32 conversion in cgemv8x4. The two paths
			// otherwise perform identical arithmetic.
			budget := float32(1e-4) + 1e-5*maxAbs
			if maxDiff > budget {
				t.Fatalf("layer %s: kernel disagrees with float-equivalent reference: maxDiff=%g maxAbs=%g budget=%g",
					tc.name, maxDiff, maxAbs, budget)
			}
			t.Logf("layer %s (%dx%d): maxDiff=%g maxAbs=%g", tc.name, n, m, maxDiff, maxAbs)
		})
	}
}

// TestOSCEBWEForwardPassInt8KernelReproducible exercises the full BBWENet
// forward pass with a deterministic sinusoidal input and asserts the output is
// bit-for-bit reproducible across runs. This catches accidental state
// dependence on previously-uninitialised buffers or platform-dependent
// quantisation rounding that would have flowed in had the int8 kernel pulled
// in non-deterministic int->float casts.
func TestOSCEBWEForwardPassInt8KernelReproducible(t *testing.T) {
	bweBlob := requireLibopusOSCEBWEModelBlob(t)
	parsed1, err := dnnblob.Clone(bweBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	parsed2, err := dnnblob.Clone(bweBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}

	var s1, s2 osceBWE.State
	if err := s1.SetModel(parsed1); err != nil {
		t.Fatalf("s1.SetModel: %v", err)
	}
	if err := s2.SetModel(parsed2); err != nil {
		t.Fatalf("s2.SetModel: %v", err)
	}

	in := make([]float32, 320)
	for i := range in {
		in[i] = float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/16000))
	}
	features := make([]float32, 2*osceBWE.FeatureDim)

	out1 := make([]float32, 3*len(in))
	out2 := make([]float32, 3*len(in))
	if err := s1.Process(in, out1, features); err != nil {
		t.Fatalf("s1.Process: %v", err)
	}
	if err := s2.Process(in, out2, features); err != nil {
		t.Fatalf("s2.Process: %v", err)
	}

	for i := range out1 {
		if out1[i] != out2[i] {
			t.Fatalf("non-reproducible output at i=%d: %g vs %g", i, out1[i], out2[i])
		}
	}
}

// referenceCGEMV8x4OnLibopusLayer reproduces the cgemv8x4 calculation in
// float arithmetic by walking the libopus int8 weight tile layout. The result
// must be identical (up to int32->float32 rounding) to the cgemv8x4 output
// for the same input.
func referenceCGEMV8x4OnLibopusLayer(layer *osceBWE.LinearLayer, in []float32) []float32 {
	n := layer.NbOutputs
	m := layer.NbInputs
	out := make([]float32, n)
	q := make([]int32, m)
	for i := 0; i < m; i++ {
		qi := int32(math.Floor(0.5 + 127*float64(in[i])))
		if qi > 127 {
			qi = 127
		} else if qi < -128 {
			qi = -128
		}
		q[i] = qi
	}
	wOffset := 0
	for row := 0; row < n; row += 8 {
		var acc [8]int64
		for col := 0; col < m; col += 4 {
			x0 := int64(q[col])
			x1 := int64(q[col+1])
			x2 := int64(q[col+2])
			x3 := int64(q[col+3])
			for r := 0; r < 8; r++ {
				base := wOffset + r*4
				acc[r] += int64(layer.Weights.At(base))*x0 +
					int64(layer.Weights.At(base+1))*x1 +
					int64(layer.Weights.At(base+2))*x2 +
					int64(layer.Weights.At(base+3))*x3
			}
			wOffset += 32
		}
		for r := 0; r < 8; r++ {
			out[row+r] = float32(acc[r]) * layer.Scale.At(row+r)
		}
	}
	if !layer.Bias.Empty() {
		for i := 0; i < n; i++ {
			out[i] += layer.Bias.At(i)
		}
	}
	return out
}

func TestDecoderOSCEBWEModelLoadsFromLibopusBlob(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+BWE) error: %v", err)
	}
	if !dec.osceBWEModelLoaded {
		t.Fatalf("decoder did not retain osceBWEModelLoaded after SetDNNBlob")
	}
	if !dec.osceBWEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE BWE runtime model after SetDNNBlob")
	}

	// Re-applying with an OSCE-BWE-free blob must clear the runtime binding
	// while keeping the core neural models loaded.
	if err := dec.SetDNNBlob(coreBlob); err != nil {
		t.Fatalf("SetDNNBlob(core-only) error: %v", err)
	}
	if dec.osceBWEModelLoaded {
		t.Fatalf("decoder unexpectedly retained osceBWEModelLoaded after core-only blob")
	}
	if dec.osceBWEModelLoadedRuntime() {
		t.Fatalf("decoder unexpectedly retained OSCE BWE runtime binding after core-only blob")
	}
}

func TestDecoderOSCEBWECrossFadeTransition(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(mono 48kHz): %v", err)
	}
	if err := dec.SetComplexity(4); err != nil {
		t.Fatalf("SetComplexity(4): %v", err)
	}
	if err := dec.SetOSCEBWE(true); err != nil {
		t.Fatalf("SetOSCEBWE(true): %v", err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+BWE): %v", err)
	}
	if !dec.osceBWEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE BWE runtime model after SetDNNBlob")
	}

	const frameSize = 960 // 20 ms @ 48 kHz
	hybridA := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)
	silkWB := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
	hybridB := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

	pcmA := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	pcmB := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	pcmC := make([]float32, dec.maxPacketSamples*int(dec.Channels()))

	// Step 1: Hybrid SWB -- BWE inactive. prevBWEActive must be false.
	gotA, err := dec.Decode(hybridA, pcmA)
	if err != nil {
		t.Fatalf("Decode(hybrid SWB #1): %v", err)
	}
	if gotA != frameSize {
		t.Fatalf("Decode(hybrid SWB #1) returned %d samples, want %d", gotA, frameSize)
	}
	if dec.osceBWE != nil && dec.osceBWE.prevBWEActive {
		t.Fatalf("prevBWEActive=true after Hybrid SWB decode (BWE should be inactive)")
	}

	// Step 2: SILK WB -- BWE active. prevBWEActive must transition to true.
	gotB, err := dec.Decode(silkWB, pcmB)
	if err != nil {
		t.Fatalf("Decode(silk WB): %v", err)
	}
	if gotB != frameSize {
		t.Fatalf("Decode(silk WB) returned %d samples, want %d", gotB, frameSize)
	}
	if dec.osceBWE == nil || !dec.osceBWE.prevBWEActive {
		t.Fatalf("prevBWEActive=false after SILK WB decode (BWE should be active)")
	}

	// Step 3: Hybrid SWB again -- BWE leaves, cross-fade out. prevBWEActive
	// must transition back to false.
	gotC, err := dec.Decode(hybridB, pcmC)
	if err != nil {
		t.Fatalf("Decode(hybrid SWB #2): %v", err)
	}
	if gotC != frameSize {
		t.Fatalf("Decode(hybrid SWB #2) returned %d samples, want %d", gotC, frameSize)
	}
	if dec.osceBWE != nil && dec.osceBWE.prevBWEActive {
		t.Fatalf("prevBWEActive=true after Hybrid SWB transition (BWE should be inactive)")
	}

	// Verify all three decoded buffers are well-formed (no NaN/Inf, no
	// runaway samples) and the cross-fade boundary at sample 0 of each
	// frame is within the in-frame dynamic range.
	checkPCMSane := func(t *testing.T, name string, pcm []float32, n int) {
		t.Helper()
		var maxAbs float32
		for i := 0; i < n; i++ {
			v := pcm[i]
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("%s: PCM contains NaN/Inf at sample %d: %v", name, i, v)
			}
			if v > maxAbs {
				maxAbs = v
			} else if -v > maxAbs {
				maxAbs = -v
			}
		}
		if maxAbs > 1.5 {
			t.Fatalf("%s: PCM exceeds [-1.5, 1.5] envelope (maxAbs=%v); cross-fade likely produced runaway samples", name, maxAbs)
		}
	}
	checkPCMSane(t, "hybrid SWB #1", pcmA, gotA)
	checkPCMSane(t, "silk WB", pcmB, gotB)
	checkPCMSane(t, "hybrid SWB #2", pcmC, gotC)

	// Sanity: the BWE cross-fade region (first 480 samples of the
	// transition frames) should produce a continuous signal. We measure the
	// maximum absolute sample-to-sample step within the cross-fade window
	// and compare it against the overall in-frame max step. If the
	// cross-fade introduced an obvious discontinuity the boundary step
	// would exceed the in-frame dynamic range by a noticeable margin.
	maxStep := func(pcm []float32, start, end int) float32 {
		var m float32
		for i := start + 1; i < end; i++ {
			d := pcm[i] - pcm[i-1]
			if d < 0 {
				d = -d
			}
			if d > m {
				m = d
			}
		}
		return m
	}
	xfadeStepB := maxStep(pcmB, 0, 480)
	fullStepB := maxStep(pcmB, 0, gotB)
	// xfade region step should not exceed the in-frame maximum (allow
	// equality -- the cross-fade lives inside the same frame).
	if xfadeStepB > fullStepB+1e-3 {
		t.Fatalf("cross-fade boundary on SILK WB frame produced step %v exceeding in-frame max %v", xfadeStepB, fullStepB)
	}
	xfadeStepC := maxStep(pcmC, 0, 480)
	fullStepC := maxStep(pcmC, 0, gotC)
	if xfadeStepC > fullStepC+1e-3 {
		t.Fatalf("cross-fade boundary on Hybrid SWB #2 frame produced step %v exceeding in-frame max %v", xfadeStepC, fullStepC)
	}
}

// TestDecoderOSCEBWEPLC verifies BWE is invoked during PLC when the previous
// packet was SILK WB. The PLC output must be non-zero (so the BWE forward
// pass did not collapse the concealed lowband to silence) and must not
// contain NaN/Inf samples.
func TestDecoderOSCEBWEPLC(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(mono 48kHz): %v", err)
	}
	if err := dec.SetComplexity(4); err != nil {
		t.Fatalf("SetComplexity(4): %v", err)
	}
	if err := dec.SetOSCEBWE(true); err != nil {
		t.Fatalf("SetOSCEBWE(true): %v", err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+BWE): %v", err)
	}
	if !dec.osceBWEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE BWE runtime model after SetDNNBlob")
	}

	const frameSize = 960 // 20 ms @ 48 kHz
	silkWB := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

	// Step 1: decode a SILK WB packet so the decoder retains valid
	// lastPacketMode/lastBandwidth for the upcoming PLC.
	pcmGood := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	gotGood, err := dec.Decode(silkWB, pcmGood)
	if err != nil {
		t.Fatalf("Decode(silk WB): %v", err)
	}
	if gotGood != frameSize {
		t.Fatalf("Decode(silk WB) returned %d samples, want %d", gotGood, frameSize)
	}
	if dec.lastPacketMode != ModeSILK || dec.lastBandwidth != BandwidthWideband {
		t.Fatalf("decoder state after good SILK WB packet: mode=%v bandwidth=%v, want SILK WB", dec.lastPacketMode, dec.lastBandwidth)
	}

	// Step 2: invoke Decode(nil) for PLC. With BWE armed, the PLC path
	// must invoke maybeApplyOSCEBWEPostSilk after the standard PLC
	// upsampling so the concealed lowband is bandwidth-extended like a
	// good SILK WB frame.
	pcmPLC := make([]float32, frameSize*dec.Channels())
	gotPLC, err := dec.Decode(nil, pcmPLC)
	if err != nil {
		t.Fatalf("Decode(nil) PLC: %v", err)
	}
	if gotPLC != frameSize {
		t.Fatalf("Decode(nil) PLC returned %d samples, want %d", gotPLC, frameSize)
	}

	// PLC output must be non-zero -- BWE on a non-silent concealed lowband
	// should produce non-trivial output. The silk_resampler upsampling on
	// its own already yields non-zero energy, so BWE-active or BWE-failed
	// both pass this check; the assertion guards against the PLC path
	// returning silence by accident.
	var energy float64
	for i := 0; i < gotPLC*dec.Channels(); i++ {
		v := pcmPLC[i]
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("PLC PCM contains NaN/Inf at sample %d: %v", i, v)
		}
		energy += float64(v) * float64(v)
	}
	if energy == 0 {
		t.Fatalf("PLC PCM is silent after BWE-armed SILK WB packet -- BWE PLC path likely did not run")
	}

	t.Run("stereo", func(t *testing.T) {
		dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(stereo 48kHz): %v", err)
		}
		if err := dec.SetComplexity(4); err != nil {
			t.Fatalf("SetComplexity(4): %v", err)
		}
		if err := dec.SetOSCEBWE(true); err != nil {
			t.Fatalf("SetOSCEBWE(true): %v", err)
		}
		if err := dec.SetDNNBlob(merged); err != nil {
			t.Fatalf("SetDNNBlob(merged core+BWE): %v", err)
		}
		if !dec.osceBWEModelLoadedRuntime() {
			t.Fatalf("decoder did not bind OSCE BWE runtime model after SetDNNBlob")
		}

		silkWB := makeValidStereoSILKPacketForFrameSizeBandwidthForOSCEBWETest(t, frameSize, BandwidthWideband)
		pcmGood := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
		gotGood, err := dec.Decode(silkWB, pcmGood)
		if err != nil {
			t.Fatalf("Decode(stereo silk WB): %v", err)
		}
		if gotGood != frameSize {
			t.Fatalf("Decode(stereo silk WB) returned %d samples, want %d", gotGood, frameSize)
		}
		if dec.lastPacketMode != ModeSILK || dec.lastBandwidth != BandwidthWideband || !dec.prevPacketStereo {
			t.Fatalf("decoder state after good stereo SILK WB packet: mode=%v bandwidth=%v stereo=%v, want SILK WB stereo", dec.lastPacketMode, dec.lastBandwidth, dec.prevPacketStereo)
		}
		if dec.osceBWE == nil || !dec.osceBWE.prevBWEActive {
			t.Fatalf("prevBWEActive=false after stereo SILK WB decode")
		}

		pcmPLC := make([]float32, frameSize*dec.Channels())
		gotPLC, err := dec.Decode(nil, pcmPLC)
		if err != nil {
			t.Fatalf("Decode(nil) stereo PLC: %v", err)
		}
		if gotPLC != frameSize {
			t.Fatalf("Decode(nil) stereo PLC returned %d samples, want %d", gotPLC, frameSize)
		}
		if dec.osceBWE == nil || !dec.osceBWE.prevBWEActive {
			t.Fatalf("prevBWEActive=false after stereo SILK WB PLC; BWE PLC path did not stay active")
		}

		var leftEnergy, rightEnergy, diffEnergy float64
		for i := 0; i < gotPLC; i++ {
			l := pcmPLC[2*i]
			r := pcmPLC[2*i+1]
			if math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) {
				t.Fatalf("stereo PLC left PCM contains NaN/Inf at sample %d: %v", i, l)
			}
			if math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) {
				t.Fatalf("stereo PLC right PCM contains NaN/Inf at sample %d: %v", i, r)
			}
			leftEnergy += float64(l) * float64(l)
			rightEnergy += float64(r) * float64(r)
			diff := float64(l - r)
			diffEnergy += diff * diff
		}
		if leftEnergy == 0 {
			t.Fatalf("stereo PLC left channel is silent after BWE-armed SILK WB packet")
		}
		if rightEnergy == 0 {
			t.Fatalf("stereo PLC right channel is silent after BWE-armed SILK WB packet")
		}
		if diffEnergy == 0 {
			t.Fatalf("stereo PLC collapsed to mono after BWE-armed stereo SILK WB packet")
		}
	})
}

func TestDecoderOSCEBWEDisableFadesOut(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	const frameSize = 960
	packetA := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
	packetB := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

	decRaw, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(raw): %v", err)
	}
	if err := decRaw.SetComplexity(4); err != nil {
		t.Fatalf("raw.SetComplexity(4): %v", err)
	}
	if err := decRaw.SetDNNBlob(merged); err != nil {
		t.Fatalf("raw.SetDNNBlob: %v", err)
	}
	if err := decRaw.SetOSCEBWE(false); err != nil {
		t.Fatalf("raw.SetOSCEBWE(false): %v", err)
	}
	pcmRaw := make([]float32, decRaw.maxPacketSamples*int(decRaw.channels))
	if n, err := decRaw.Decode(packetA, pcmRaw); err != nil {
		t.Fatalf("raw.Decode #1: %v", err)
	} else if n != frameSize {
		t.Fatalf("raw.Decode #1 samples=%d want %d", n, frameSize)
	}
	if n, err := decRaw.Decode(packetB, pcmRaw); err != nil {
		t.Fatalf("raw.Decode #2: %v", err)
	} else if n != frameSize {
		t.Fatalf("raw.Decode #2 samples=%d want %d", n, frameSize)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(active): %v", err)
	}
	if err := dec.SetComplexity(4); err != nil {
		t.Fatalf("active.SetComplexity(4): %v", err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("active.SetDNNBlob: %v", err)
	}
	if err := dec.SetOSCEBWE(true); err != nil {
		t.Fatalf("active.SetOSCEBWE(true): %v", err)
	}
	pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	if n, err := dec.Decode(packetA, pcm); err != nil {
		t.Fatalf("active.Decode #1: %v", err)
	} else if n != frameSize {
		t.Fatalf("active.Decode #1 samples=%d want %d", n, frameSize)
	}
	if dec.osceBWE == nil || !dec.osceBWE.prevBWEActive {
		t.Fatalf("prevBWEActive=false after active SILK WB decode")
	}
	if err := dec.SetOSCEBWE(false); err != nil {
		t.Fatalf("active.SetOSCEBWE(false): %v", err)
	}
	if n, err := dec.Decode(packetB, pcm); err != nil {
		t.Fatalf("active.Decode after disable: %v", err)
	} else if n != frameSize {
		t.Fatalf("active.Decode after disable samples=%d want %d", n, frameSize)
	}
	if dec.osceBWE != nil && dec.osceBWE.prevBWEActive {
		t.Fatalf("prevBWEActive=true after disabled SILK WB fade-out")
	}

	diff, maxDiff := float32BufferDiff(pcm[:frameSize], pcmRaw[:frameSize])
	if diff == 0 {
		t.Fatal("disabled-after-active SILK WB output is raw; BWE fade-out did not run")
	}
	t.Logf("BWE disable fade-out altered %d/%d samples; max abs diff %g", diff, frameSize, maxDiff)
}

func TestDecoderOSCEBWERuntimeIntegration(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder(stereo 48kHz): %v", err)
	}
	if err := dec.SetComplexity(4); err != nil {
		t.Fatalf("SetComplexity(4): %v", err)
	}

	if err := dec.SetOSCEBWE(true); err != nil {
		t.Fatalf("SetOSCEBWE(true): %v", err)
	}
	enabled, err := dec.OSCEBWE()
	if err != nil {
		t.Fatalf("OSCEBWE(): %v", err)
	}
	if !enabled {
		t.Fatalf("OSCEBWE() == false after SetOSCEBWE(true)")
	}

	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+BWE): %v", err)
	}
	if !dec.osceBWEModelLoaded {
		t.Fatalf("decoder did not retain osceBWEModelLoaded after SetDNNBlob")
	}
	if !dec.osceBWEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE BWE runtime model after SetDNNBlob")
	}

	// Hybrid SWB packets are encoded mono; the stereo decoder will up-mix.
	// Hybrid mode does NOT satisfy OSCE_MODE_SILK_BBWE so the BWE hook must
	// short-circuit and leave the standard hybrid upsampler output untouched.
	t.Run("hybrid_swb_bypasses_bwe", func(t *testing.T) {
		const frameSize = 960 // 20 ms @ 48 kHz
		packet := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeHybrid || toc.Bandwidth != BandwidthSuperwideband {
			t.Fatalf("unexpected TOC: mode=%v bandwidth=%v", toc.Mode, toc.Bandwidth)
		}

		pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(hybrid SWB packet): %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode(hybrid SWB packet) returned %d samples per channel, want %d", got, frameSize)
		}

		// PCM must be non-zero -- BWE must not have clobbered the standard
		// hybrid upsampler output (Hybrid mode does not satisfy
		// OSCE_MODE_SILK_BBWE in libopus).
		var energy float64
		for _, v := range pcm[:got*dec.Channels()] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("decoded PCM contains NaN/Inf: %v", v)
			}
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("decoded PCM is silent after BWE-armed Hybrid SWB decode -- standard upsampler output was unexpectedly clobbered")
		}
	})

	// SILK WB packets DO satisfy OSCE_MODE_SILK_BBWE so the BWE hook
	// should overwrite the standard silk_resampler output with the BWE
	// 16k -> 48k forward pass. The packet is mono so the stereo decoder's
	// channels==2 branch in maybeApplyOSCEBWEPostSilk applies. Verify the
	// decode completes and PCM is non-zero.
	t.Run("silk_wb_invokes_bwe", func(t *testing.T) {
		// Reset to clear any prior packet state.
		dec.Reset()
		const frameSize = 960 // 20 ms @ 48 kHz
		packet := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
			t.Fatalf("unexpected TOC: mode=%v bandwidth=%v", toc.Mode, toc.Bandwidth)
		}

		pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(silk WB packet): %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode(silk WB packet) returned %d samples per channel, want %d", got, frameSize)
		}

		var energy float64
		for _, v := range pcm[:got*dec.Channels()] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("decoded PCM contains NaN/Inf: %v", v)
			}
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("decoded PCM is silent after BWE-armed SILK WB decode")
		}
	})

	// Stereo SILK WB packets also satisfy OSCE_MODE_SILK_BBWE. libopus runs
	// `osce_bwe(...)` independently on each channel with its own
	// `silk_OSCE_BWE_struct`. The gopus runtime mirrors that with
	// `osceBWERuntime[0]` (mid/left) and `osceBWERuntime[1]` (side/right)
	// in decoderOSCEBWEState. Verify both channels carry non-zero BWE
	// output and the channels differ (the stereo test packet is encoded
	// with distinct L/R content so a per-channel BWE pass must produce
	// distinguishable outputs).
	t.Run("stereo_silk_wb_invokes_bwe", func(t *testing.T) {
		// Reset to clear any prior packet state from the previous sub-test.
		dec.Reset()
		const frameSize = 960 // 20 ms @ 48 kHz
		packet := makeValidStereoSILKPacketForFrameSizeBandwidthForOSCEBWETest(t, frameSize, BandwidthWideband)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
			t.Fatalf("unexpected TOC: mode=%v bandwidth=%v", toc.Mode, toc.Bandwidth)
		}
		if !toc.Stereo {
			t.Fatalf("expected stereo packet but TOC.Stereo=false")
		}

		pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(stereo silk WB packet): %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode(stereo silk WB packet) returned %d samples per channel, want %d", got, frameSize)
		}

		var leftEnergy, rightEnergy, diffEnergy float64
		for i := 0; i < got; i++ {
			l := pcm[2*i]
			r := pcm[2*i+1]
			if math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) {
				t.Fatalf("decoded left PCM contains NaN/Inf at i=%d: %v", i, l)
			}
			if math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) {
				t.Fatalf("decoded right PCM contains NaN/Inf at i=%d: %v", i, r)
			}
			leftEnergy += float64(l) * float64(l)
			rightEnergy += float64(r) * float64(r)
			diff := float64(l - r)
			diffEnergy += diff * diff
		}
		if leftEnergy == 0 {
			t.Fatalf("decoded left channel PCM is silent after stereo BWE-armed SILK WB decode")
		}
		if rightEnergy == 0 {
			t.Fatalf("decoded right channel PCM is silent after stereo BWE-armed SILK WB decode")
		}
		// The stereo test packet uses different signals per channel so the
		// per-channel BWE forward pass should produce distinguishable
		// output. If both channels were identical (e.g. because the side-
		// channel runtime was not invoked) diffEnergy would be exactly zero.
		if diffEnergy == 0 {
			t.Fatalf("decoded stereo PCM is mono (left == right) after stereo BWE pass -- side-channel runtime not invoked?")
		}
	})
}

func TestDecoderOSCEBWEComplexityGate(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	const frameSize = 960
	packet := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

	for _, tc := range []struct {
		name       string
		complexity int
		wantActive bool
	}{
		{name: "below_gate", complexity: 3, wantActive: false},
		{name: "at_gate", complexity: 4, wantActive: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if err := dec.SetComplexity(tc.complexity); err != nil {
				t.Fatalf("SetComplexity(%d): %v", tc.complexity, err)
			}
			if err := dec.SetOSCEBWE(true); err != nil {
				t.Fatalf("SetOSCEBWE(true): %v", err)
			}
			if err := dec.SetDNNBlob(merged); err != nil {
				t.Fatalf("SetDNNBlob(merged core+BWE): %v", err)
			}
			pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
			if got, err := dec.Decode(packet, pcm); err != nil {
				t.Fatalf("Decode(silk WB): %v", err)
			} else if got != frameSize {
				t.Fatalf("Decode returned %d samples per channel, want %d", got, frameSize)
			}
			gotActive := dec.osceBWE != nil && dec.osceBWE.prevBWEActive
			if gotActive != tc.wantActive {
				t.Fatalf("prevBWEActive=%v want %v at complexity %d", gotActive, tc.wantActive, tc.complexity)
			}
		})
	}
}

// makeValidStereoSILKPacketForFrameSizeBandwidthForOSCEBWETest synthesises a
// stereo SILK packet at the given frame size / bandwidth with distinct L/R
// content so the per-channel OSCE BWE forward pass produces distinguishable
// output. Mirrors the mono helper in decoder_dred_test.go but with two-channel
// input.
func makeValidStereoSILKPacketForFrameSizeBandwidthForOSCEBWETest(t *testing.T, frameSize int, bandwidth Bandwidth) []byte {
	t.Helper()

	if frameSize != 480 && frameSize != 960 && frameSize != 1920 && frameSize != 2880 {
		t.Fatalf("silk stereo BWE test packet requires 10/20/40/60ms frame size, got %d", frameSize)
	}
	if bandwidth != BandwidthNarrowband && bandwidth != BandwidthMediumband && bandwidth != BandwidthWideband {
		t.Fatalf("silk stereo BWE test packet requires NB/MB/WB bandwidth, got %v", bandwidth)
	}

	enc := internalenc.NewEncoder(48000, 2)
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.Bandwidth(bandwidth))
	enc.SetBitrate(48000)
	enc.SetForceChannels(2)

	pcm := make([]float32, frameSize*2)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		l := 0.31*math.Sin(2*math.Pi*197*tm) + 0.12*math.Sin(2*math.Pi*389*tm+0.23)
		// Distinct right-channel content (different frequencies / phase) so
		// the per-channel BWE forward pass should produce L != R.
		r := 0.27*math.Sin(2*math.Pi*263*tm+0.41) + 0.14*math.Sin(2*math.Pi*431*tm+0.07)
		pcm[2*i] = float32(l)
		pcm[2*i+1] = float32(r)
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode(stereo SILK): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(stereo SILK) returned empty packet")
	}
	toc := ParseTOC(packet[0])
	if toc.Mode != ModeSILK || toc.Bandwidth != bandwidth || toc.FrameSize != frameSize {
		t.Fatalf("Encode(stereo SILK) produced mode=%v bandwidth=%v frame=%d, want mode=%v bandwidth=%v frame=%d", toc.Mode, toc.Bandwidth, toc.FrameSize, ModeSILK, bandwidth, frameSize)
	}
	if !toc.Stereo {
		t.Fatalf("Encode(stereo SILK) produced mono packet (TOC.Stereo=false) despite ForceChannels=2")
	}
	return packet
}

func TestOSCEBWEVariableSequenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getLibopusOSCEBWEForwardHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE BWE sequence", err)
	}
	parsed, err := dnnblob.Clone(requireLibopusOSCEBWEModelBlob(t))
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	const frames = 12
	input := make([]int16, frames*160)
	for frame := 0; frame < frames; frame++ {
		for i := 0; i < 160; i++ {
			n := frame*160 + i
			v := 0.47*math.Sin(2*math.Pi*313*float64(n)/16000) +
				0.18*math.Sin(2*math.Pi*719*float64(n)/16000+0.23) +
				0.08*math.Sin(2*math.Pi*97*float64(n)/16000+float64(frame)*0.11)
			q := int(math.Round(v * 32767))
			if q > 32767 {
				q = 32767
			} else if q < -32768 {
				q = -32768
			}
			input[n] = int16(q)
		}
	}

	payload := make([]byte, 12+2*len(input))
	copy(payload, "BSEQ")
	binary.LittleEndian.PutUint32(payload[4:8], 1)
	binary.LittleEndian.PutUint32(payload[8:12], frames)
	for i, v := range input {
		binary.LittleEndian.PutUint16(payload[12+2*i:], uint16(v))
	}
	out, err := libopustest.RunHelperArgs(helperPath, payload, "0", "sequence")
	if err != nil {
		t.Fatalf("OSCE BWE sequence oracle: %v", err)
	}
	reader, version, err := libopustest.NewOracleReaderMagicVersion("OSCE BWE sequence", "BSEQO\x00\x00\x00", out)
	if err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatalf("helper version=%d, want 4", version)
	}
	if got := reader.I32(); got != frames {
		t.Fatalf("helper frames=%d, want %d", got, frames)
	}
	if got := reader.I32(); got != frames*480 {
		t.Fatalf("helper output samples=%d, want %d", got, frames*480)
	}
	const traceFloats = 1701
	const traceInts = 21
	if got := reader.I32(); got != traceFloats {
		t.Fatalf("helper state floats/frame=%d, want %d", got, traceFloats)
	}
	if got := reader.I32(); got != traceInts {
		t.Fatalf("helper delay ints/frame=%d, want %d", got, traceInts)
	}
	const latentFloats = 256
	if got := reader.I32(); got != latentFloats {
		t.Fatalf("helper latent floats/frame=%d, want %d", got, latentFloats)
	}
	const af3DenseFloats = 50
	if got := reader.I32(); got != af3DenseFloats {
		t.Fatalf("helper AF3 dense floats/frame=%d, want %d", got, af3DenseFloats)
	}
	reader.ExpectRemaining(frames*osceBWE.FeatureDim*4 + frames*latentFloats*4 + frames*af3DenseFloats*4 + frames*480*4 + frames*traceFloats*4 + frames*traceInts*2)
	wantFeatures := make([]float32, frames*osceBWE.FeatureDim)
	for i := range wantFeatures {
		wantFeatures[i] = reader.Float32()
	}
	for range frames * latentFloats {
		reader.Float32()
	}
	for range frames * af3DenseFloats {
		reader.Float32()
	}
	wantOutput := make([]float32, frames*480)
	for i := range wantOutput {
		wantOutput[i] = reader.Float32()
	}
	for range frames * traceFloats {
		reader.Float32()
	}
	for range frames * traceInts {
		reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	var featureState osceBWE.FeatureState
	featureState.Reset()
	var runtime osceBWE.State
	if err := runtime.SetModel(parsed); err != nil {
		t.Fatalf("State.SetModel: %v", err)
	}
	var inputFloat [160]float32
	var features [osceBWE.FeatureDim]float32
	var output [480]float32
	for frame := 0; frame < frames; frame++ {
		frameInput := input[frame*160 : frame*160+160]
		for i, v := range frameInput {
			inputFloat[i] = float32(v) / 32768
		}
		featureState.CalculateFeatures(features[:], frameInput)
		for i, got := range features {
			want := wantFeatures[frame*osceBWE.FeatureDim+i]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame=%d feature[%d] Go=%08x C=%08x", frame, i, math.Float32bits(got), math.Float32bits(want))
			}
		}
		if err := runtime.ProcessDelayed(inputFloat[:], output[:], features[:]); err != nil {
			t.Fatalf("frame=%d State.ProcessDelayed: %v", frame, err)
		}
		for i, got := range output {
			want := wantOutput[frame*480+i]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame=%d sample=%d Go=%08x C=%08x", frame, i, math.Float32bits(got), math.Float32bits(want))
			}
		}
	}
}

var libopusOSCEDecodeSingleHelper libopustest.HelperCache

// getLibopusOSCEDecodeSingleHelperPath lazily builds the OSCE decode oracle.
func getLibopusOSCEDecodeSingleHelperPath() (string, error) {
	return cachedLibopusOSCEHelperPath(
		&libopusOSCEDecodeSingleHelper,
		"libopus_osce_decode_single.c",
		"gopus_libopus_osce_decode_single",
		false, // no internal libopus header access needed
	)
}

// runLibopusOSCEDecodeSingle invokes the libopus OSCE decode oracle on
// `packets` and returns the flat float32 PCM from libopus.
// The helper uses the LACE/NoLACE/BWE weights that are compiled statically
// into the OSCE-enabled libopus build; no runtime blob loading is needed.
func runLibopusOSCEDecodeSingle(
	binPath string,
	sampleRate, channels, frameSize, complexity int,
	enableBWE bool,
	packets [][]byte,
) ([]float32, error) {
	var bweFlag uint32
	if enableBWE {
		bweFlag = 1
	}

	// Build the binary input payload.
	var buf []byte
	put32 := func(v uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], v)
		buf = append(buf, b[:]...)
	}
	putBytes := func(b []byte) { buf = append(buf, b...) }

	putBytes([]byte("GSOI"))
	put32(1) // version
	put32(uint32(sampleRate))
	put32(uint32(channels))
	put32(uint32(frameSize))
	put32(uint32(complexity))
	put32(bweFlag)
	put32(uint32(len(packets)))
	for _, pkt := range packets {
		put32(uint32(len(pkt)))
		putBytes(pkt)
	}

	out, err := libopustest.RunHelper(binPath, buf)
	if err != nil {
		return nil, fmt.Errorf("libopus OSCE decode oracle: %w", err)
	}
	reader, version, err := libopustest.NewOracleReaderMagicVersion("OSCE decode", "GSOO", out)
	if err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("OSCE decode oracle version=%d want 1", version)
	}
	totalSamples := int(reader.U32())
	if reader.Err() != nil {
		return nil, reader.Err()
	}
	reader.ExpectRemaining(totalSamples * 4)
	pcm := make([]float32, totalSamples)
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return pcm, nil
}

func TestOSCEEndToEndSampleParity(t *testing.T) {
	libopustest.RequireOracle(t)

	binPath, err := getLibopusOSCEDecodeSingleHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE decode single", err)
	}

	laceBlob := requireLibopusOSCELACEModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)

	// Merged model blob for gopus: core + LACE + BWE.
	mergedAll := make([]byte, 0, len(coreBlob)+len(laceBlob)+len(bweBlob))
	mergedAll = append(mergedAll, coreBlob...)
	mergedAll = append(mergedAll, laceBlob...)
	mergedAll = append(mergedAll, bweBlob...)

	// Merged blob without BWE (for LACE/NoLACE-only subtests).
	mergedLACE := make([]byte, 0, len(coreBlob)+len(laceBlob))
	mergedLACE = append(mergedLACE, coreBlob...)
	mergedLACE = append(mergedLACE, laceBlob...)

	const (
		frameSize  = 960 // 20 ms @ 48 kHz
		numPackets = 24  // fade-in and recurrent state across varied packets
		sampleRate = 48000
	)

	// Encode a mono SILK WB test sequence (same signal used by forward-pass tests).
	encodeMonoSILKWB := func(t *testing.T, n int) [][]byte {
		t.Helper()
		enc := internalenc.NewEncoder(48000, 1)
		enc.SetMode(internalenc.ModeSILK)
		enc.SetBandwidth(types.BandwidthWideband)
		enc.SetBitrate(40000)
		var packets [][]byte
		for i := 0; i < n; i++ {
			pcm := make([]float32, frameSize)
			for j := 0; j < frameSize; j++ {
				tm := float64(i*frameSize+j) / 48000.0
				pcm[j] = float32(0.3*math.Sin(2*math.Pi*197*tm) +
					0.12*math.Sin(2*math.Pi*389*tm+0.23))
			}
			pkt, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode mono SILK WB packet %d: %v", i, err)
			}
			if len(pkt) == 0 {
				t.Fatalf("Encode mono SILK WB packet %d: empty", i)
			}
			packets = append(packets, append([]byte(nil), pkt...))
		}
		return packets
	}

	// Encode a stereo SILK WB test sequence.
	encodeStereoSILKWB := func(t *testing.T, n int) [][]byte {
		t.Helper()
		enc := internalenc.NewEncoder(48000, 2)
		enc.SetMode(internalenc.ModeSILK)
		enc.SetBandwidth(types.BandwidthWideband)
		enc.SetBitrate(48000)
		enc.SetForceChannels(2)
		var packets [][]byte
		for i := 0; i < n; i++ {
			pcm := make([]float32, frameSize*2)
			for j := 0; j < frameSize; j++ {
				tm := float64(i*frameSize+j) / 48000.0
				l := 0.31*math.Sin(2*math.Pi*197*tm) + 0.12*math.Sin(2*math.Pi*389*tm+0.23)
				r := 0.27*math.Sin(2*math.Pi*263*tm+0.41) + 0.14*math.Sin(2*math.Pi*431*tm+0.07)
				pcm[2*j] = float32(l)
				pcm[2*j+1] = float32(r)
			}
			pkt, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode stereo SILK WB packet %d: %v", i, err)
			}
			if len(pkt) == 0 {
				t.Fatalf("Encode stereo SILK WB packet %d: empty", i)
			}
			packets = append(packets, append([]byte(nil), pkt...))
		}
		return packets
	}

	// decodeGopus decodes packets with gopus, armed with the given merged blob
	// and controls.
	decodeGopus := func(t *testing.T, packets [][]byte, channels, complexity int,
		enableBWE, enableLACE bool, mergedBlob []byte) []float32 {
		t.Helper()
		dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
		if err != nil {
			t.Fatalf("NewDecoder(%d ch): %v", channels, err)
		}
		if err := dec.SetComplexity(complexity); err != nil {
			t.Fatalf("SetComplexity(%d): %v", complexity, err)
		}
		if err := dec.SetOSCEBWE(enableBWE); err != nil {
			t.Fatalf("SetOSCEBWE(%v): %v", enableBWE, err)
		}
		if err := dec.SetOSCELACE(enableLACE); err != nil {
			t.Fatalf("SetOSCELACE(%v): %v", enableLACE, err)
		}
		if err := dec.SetDNNBlob(mergedBlob); err != nil {
			t.Fatalf("SetDNNBlob: %v", err)
		}
		totalSamples := frameSize * channels * len(packets)
		out := make([]float32, totalSamples)
		offset := 0
		for i, pkt := range packets {
			pcm := out[offset : offset+frameSize*channels]
			n, err := dec.Decode(pkt, pcm)
			if err != nil {
				t.Fatalf("Decode packet %d: %v", i, err)
			}
			if n != frameSize {
				t.Fatalf("Decode packet %d count=%d, want %d", i, n, frameSize)
			}
			offset += n * channels
		}
		// The same decoder and caller-owned PCM exercise active recurrent state.
		warmPCM := make([]float32, frameSize*channels)
		idx := 0
		if allocs := testing.AllocsPerRun(20, func() {
			n, err := dec.Decode(packets[idx], warmPCM)
			if err != nil || n != frameSize {
				t.Fatalf("warm Decode returned %d, %v", n, err)
			}
			idx = (idx + 1) % len(packets)
		}); allocs != 0 {
			t.Fatalf("warm decode allocations=%g, want 0", allocs)
		}
		if rmsOfFloat32(warmPCM) == 0 {
			t.Fatal("warm decoder output has zero energy")
		}
		for i, v := range warmPCM {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("warm PCM[%d]=%v is not finite", i, v)
			}
		}
		// A malformed multiframe SILK packet must leave no callback installed
		// after Decode returns. Reset then replays a complete valid history.
		badPacket := []byte{0x4b, 0x83, 0x02, 0x01, 0, 0}
		if channels == 2 {
			badPacket[0] |= 4
		}
		errorPCM := make([]float32, 2880*channels)
		if _, err := dec.Decode(badPacket, errorPCM); err != ErrInvalidPacket {
			t.Fatalf("malformed Decode error=%v, want ErrInvalidPacket", err)
		}
		featuresBefore := dec.osceLACE.osceLACEFeatures
		if channels == 2 {
			_, err = dec.silkDecoder.DecodeStereo(packets[0][1:], silk.BandwidthWideband, frameSize, true)
		} else {
			_, err = dec.silkDecoder.Decode(packets[0][1:], silk.BandwidthWideband, frameSize, true)
		}
		if err != nil {
			t.Fatalf("SILK decode after public error: %v", err)
		}
		if dec.osceLACE.osceLACEFeatures != featuresBefore {
			t.Fatal("OSCE callback remains active after public Decode error")
		}
		dec.Reset()
		for frame, pkt := range packets {
			n, err := dec.Decode(pkt, warmPCM)
			if err != nil || n != frameSize {
				t.Fatalf("reset Decode frame=%d returned %d, %v", frame, n, err)
			}
			for i, v := range warmPCM {
				want := out[frame*len(warmPCM)+i]
				if math.Float32bits(v) != math.Float32bits(want) {
					t.Fatalf("reset frame=%d sample=%d Go=%08x initial=%08x", frame, i, math.Float32bits(v), math.Float32bits(want))
				}
			}
		}
		return out[:offset]
	}

	subtests := []struct {
		name       string
		channels   int
		complexity int
		enableBWE  bool
		enableLACE bool
		mergedBlob func() []byte
		qualBar    qualitycompare.QualityBar
	}{
		{
			name: "lace_mono", channels: 1, complexity: 6,
			enableBWE: false, enableLACE: true,
			mergedBlob: func() []byte { return mergedLACE },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "nolace_mono", channels: 1, complexity: 7,
			enableBWE: false, enableLACE: true,
			mergedBlob: func() []byte { return mergedLACE },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "bwe_mono", channels: 1, complexity: 4,
			enableBWE: true, enableLACE: false,
			mergedBlob: func() []byte { return mergedAll },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "lace_stereo", channels: 2, complexity: 6,
			enableBWE: false, enableLACE: true,
			mergedBlob: func() []byte { return mergedLACE },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "nolace_stereo", channels: 2, complexity: 7,
			enableBWE: false, enableLACE: true,
			mergedBlob: func() []byte { return mergedLACE },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "bwe_stereo", channels: 2, complexity: 4,
			enableBWE: true, enableLACE: false,
			mergedBlob: func() []byte { return mergedAll },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
		{
			name: "nolace_bwe_stereo", channels: 2, complexity: 7,
			enableBWE: true, enableLACE: true,
			mergedBlob: func() []byte { return mergedAll },
			qualBar:    qualitycompare.QualityBarNearExact,
		},
	}

	for _, tc := range subtests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var packets [][]byte
			if tc.channels == 1 {
				packets = encodeMonoSILKWB(t, numPackets)
			} else {
				packets = encodeStereoSILKWB(t, numPackets)
			}

			// Verify encoded packets are the right mode/bandwidth.
			for i, pkt := range packets {
				toc := ParseTOC(pkt[0])
				if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
					t.Fatalf("packet %d: want SILK WB, got mode=%v bw=%v", i, toc.Mode, toc.Bandwidth)
				}
				if tc.channels == 2 && !toc.Stereo {
					t.Fatalf("packet %d: want stereo, got mono TOC", i)
				}
			}

			// Get libopus reference decode (OSCE-enabled build uses static weights).
			libopusPCM, err := runLibopusOSCEDecodeSingle(
				binPath,
				sampleRate, tc.channels, frameSize, tc.complexity, tc.enableBWE,
				packets,
			)
			if err != nil {
				t.Fatalf("libopus OSCE decode: %v", err)
			}
			if len(libopusPCM) == 0 {
				t.Fatal("libopus OSCE decode: empty PCM")
			}

			// Get gopus decode.
			gopusPCM := decodeGopus(t, packets, tc.channels, tc.complexity,
				tc.enableBWE, tc.enableLACE, tc.mergedBlob())
			if len(gopusPCM) == 0 {
				t.Fatal("gopus OSCE decode: empty PCM")
			}

			// Verify both sides have non-zero energy.
			if rmsOfFloat32(gopusPCM) == 0 {
				t.Fatal("gopus OSCE decode: zero energy output")
			}
			if rmsOfFloat32(libopusPCM) == 0 {
				t.Fatal("libopus OSCE decode: zero energy output")
			}

			// Confirm no NaN/Inf in either output.
			for i, v := range gopusPCM {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					t.Fatalf("gopus PCM[%d]=%v is not finite", i, v)
				}
			}
			for i, v := range libopusPCM {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					t.Fatalf("libopus PCM[%d]=%v is not finite", i, v)
				}
			}

			if len(gopusPCM) != len(libopusPCM) {
				t.Fatalf("PCM length Go=%d C=%d", len(gopusPCM), len(libopusPCM))
			}
			n := len(gopusPCM)
			for i := range gopusPCM {
				if math.Float32bits(gopusPCM[i]) != math.Float32bits(libopusPCM[i]) {
					t.Fatalf("frame=%d sample=%d Go=%08x C=%08x", i/(frameSize*tc.channels), i%(frameSize*tc.channels), math.Float32bits(gopusPCM[i]), math.Float32bits(libopusPCM[i]))
				}
			}

			// Primary oracle: opus_compare Q metric via qualitycompare.
			cmp, err := qualitycompare.CompareDecodedFloat32(
				gopusPCM[:n], libopusPCM[:n],
				sampleRate, tc.channels, 96,
			)
			if err != nil {
				// opus_compare unavailable: fall back to waveform correlation
				// only and log the gap so it can be investigated later.
				t.Logf("opus_compare unavailable, using waveform-only fallback: %v", err)
				var corr float64
				sumA, sumB, sumASq, sumBSq, cov := float64(0), float64(0), float64(0), float64(0), float64(0)
				for i := 0; i < n; i++ {
					fa, fb := float64(gopusPCM[i]), float64(libopusPCM[i])
					sumA += fa
					sumB += fb
					sumASq += fa * fa
					sumBSq += fb * fb
				}
				meanA := sumA / float64(n)
				meanB := sumB / float64(n)
				varA, varB := float64(0), float64(0)
				for i := 0; i < n; i++ {
					da := float64(gopusPCM[i]) - meanA
					db := float64(libopusPCM[i]) - meanB
					cov += da * db
					varA += da * da
					varB += db * db
				}
				if varA > 0 && varB > 0 {
					corr = cov / math.Sqrt(varA*varB)
				}
				t.Logf("%s: corr=%.6f (waveform-only; bar corr>=%.3f)", tc.name, corr, tc.qualBar.MinCorr)
				if tc.qualBar.MinCorr > 0 && corr < tc.qualBar.MinCorr {
					t.Fatalf("%s: waveform correlation %.6f < %.6f", tc.name, corr, tc.qualBar.MinCorr)
				}
				return
			}

			qualitycompare.AssertQuality(t, cmp,
				tc.qualBar,
				fmt.Sprintf("OSCE end-to-end %s", tc.name),
			)
		})
	}
}

func TestOSCEFloatToInt16MatchesLibopusOutputScaleExhaustiveGrid(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := make([]float32, 0, 65536)
	for i := -32768; i <= 32767; i++ {
		samples = append(samples, float32(i)*(1.0/32768.0))
	}
	want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeOSCEOutputScale, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float quant", err)
	}
	for i, sample := range samples {
		if got := osceFloatToInt16(sample); got != want[i] {
			raw := i - 32768
			t.Fatalf("osceFloatToInt16(%d/32768)=%d want %d", raw, got, want[i])
		}
	}
}

func TestOSCEFloatToInt16MatchesLibopusOutputScaleTiesAndClamps(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		float32(-32769.0 / 32768.0),
		-1,
		float32(-32767.5 / 32768.0),
		float32(-32766.5 / 32768.0),
		float32(-3.5 / 32768.0),
		float32(-2.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		float32(2.5 / 32768.0),
		float32(3.5 / 32768.0),
		float32(32766.5 / 32768.0),
		float32(32767.5 / 32768.0),
		1,
		float32(32768.5 / 32768.0),
	}
	want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeOSCEOutputScale, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float quant", err)
	}
	for i, sample := range samples {
		if got := osceFloatToInt16(sample); got != want[i] {
			t.Fatalf("osceFloatToInt16(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}

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

	cases := []struct {
		name string
		mode string
	}{
		{"LACE", "lace"},
		{"NoLACE", "nolace"},
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

			for i := 0; i < inputLen; i++ {
				if math.IsNaN(float64(out[i])) || math.IsInf(float64(out[i]), 0) {
					t.Fatalf("gopus %s output[%d]=%v is not finite", tc.mode, i, out[i])
				}
				if math.IsNaN(float64(refOut[i])) || math.IsInf(float64(refOut[i]), 0) {
					t.Fatalf("libopus %s reference[%d]=%v is not finite", tc.mode, i, refOut[i])
				}
				if gotBits, wantBits := math.Float32bits(out[i]), math.Float32bits(refOut[i]); gotBits != wantBits {
					t.Fatalf("OSCE %s forward-pass first difference at sample %d: Go=%08x libopus=%08x", tc.name, i, gotBits, wantBits)
				}
			}
			t.Logf("OSCE %s forward-pass output is bit-exact", tc.name)
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
		for j := range got.Values {
			gotBits, wantBits := math.Float32bits(got.Values[j]), math.Float32bits(ref.Values[j])
			if gotBits != wantBits {
				t.Fatalf("LACE first selected-C stage difference: stage=%s subframe=%d value=%d Go=%08x C=%08x",
					traceStageName(got.Stage), got.Subframe, j, gotBits, wantBits)
			}
		}
	}
	t.Log("all captured LACE stages match selected libopus bit-for-bit")
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
	for i := range gotRecords {
		got := gotRecords[i]
		ref := refRecords[i]
		if got.Stage != ref.Stage || len(got.Values) != len(ref.Values) {
			t.Fatalf("trace record %d shape mismatch: got stage=%d len=%d; want stage=%d len=%d",
				i, got.Stage, len(got.Values), ref.Stage, len(ref.Values))
		}
		for j := range got.Values {
			gotBits, wantBits := math.Float32bits(got.Values[j]), math.Float32bits(ref.Values[j])
			if gotBits != wantBits {
				t.Fatalf("NoLACE first selected-C stage difference: stage=%s subframe=%d value=%d Go=%08x C=%08x",
					traceStageName(got.Stage), got.Subframe, j, gotBits, wantBits)
			}
		}
	}
	t.Log("all captured NoLACE stages match selected libopus bit-for-bit")
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
	if version != 2 {
		return nil, fmt.Errorf("libopus OSCE LACE forward version=%d, want 2", version)
	}
	modeID := int(reader.I32())
	arch := int(reader.I32())
	if err := verifyLibopusOSCEArch(arch); err != nil {
		return nil, err
	}
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
	if version != 2 {
		return nil, fmt.Errorf("libopus OSCE LACE trace version=%d, want 2", version)
	}
	modeID := int(reader.I32())
	arch := int(reader.I32())
	if err := verifyLibopusOSCEArch(arch); err != nil {
		return nil, err
	}
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

func verifyLibopusOSCEArch(arch int) error {
	if runtime.GOARCH != "amd64" {
		return nil
	}
	want := 0
	if dnnmath.X86VectorKernels {
		want = 4
	}
	if arch != want {
		return fmt.Errorf("libopus OSCE selected arch=%d, Go amd64 SIMD lane expects arch=%d (X86VectorKernels=%t)", arch, want, dnnmath.X86VectorKernels)
	}
	return nil
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

func TestOSCELACEForwardPassSmoke(t *testing.T) {
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
	if !model.Loaded() {
		t.Fatalf("model.Loaded() == false after Load")
	}

	const inputLen = 320 // 20 ms @ 16 kHz
	in := make([]float32, inputLen)
	for i := 0; i < inputLen; i++ {
		in[i] = float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/16000))
	}

	// Features (4 subframes * 93 OSCE features), numbits (2), periods (4).
	features := make([]float32, 4*93)
	numbits := []float32{200.0, 200.0}
	periods := []int{60, 60, 60, 60}

	t.Run("LACE", func(t *testing.T) {
		var state osceLACE.LACEState
		if err := state.SetModel(model); err != nil {
			t.Fatalf("LACEState.SetModel: %v", err)
		}
		if !state.Loaded() {
			t.Fatalf("LACEState.Loaded() == false after SetModel")
		}

		out := make([]float32, inputLen)
		if err := state.Process(in, out, features, numbits, periods); err != nil {
			t.Fatalf("LACEState.Process: %v", err)
		}

		if len(out) != inputLen {
			t.Fatalf("LACE output length: got %d, want %d", len(out), inputLen)
		}

		var energy float64
		for i, v := range out {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("LACE output[%d] is NaN/Inf: %v", i, v)
			}
			if v < -8 || v > 8 {
				t.Fatalf("LACE output[%d] out of range [-8,8]: %v", i, v)
			}
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("LACE output energy is zero -- forward pass produced silence for a non-zero input")
		}
	})

	t.Run("NoLACE", func(t *testing.T) {
		var state osceLACE.NoLACEState
		if err := state.SetModel(model); err != nil {
			t.Fatalf("NoLACEState.SetModel: %v", err)
		}
		if !state.Loaded() {
			t.Fatalf("NoLACEState.Loaded() == false after SetModel")
		}

		out := make([]float32, inputLen)
		if err := state.Process(in, out, features, numbits, periods); err != nil {
			t.Fatalf("NoLACEState.Process: %v", err)
		}

		if len(out) != inputLen {
			t.Fatalf("NoLACE output length: got %d, want %d", len(out), inputLen)
		}

		var energy float64
		for i, v := range out {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("NoLACE output[%d] is NaN/Inf: %v", i, v)
			}
			if v < -8 || v > 8 {
				t.Fatalf("NoLACE output[%d] out of range [-8,8]: %v", i, v)
			}
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("NoLACE output energy is zero -- forward pass produced silence for a non-zero input")
		}
	})
}

// TestOSCELACEForwardPassRejectsBadInputs verifies the Process API rejects
// inputs that don't match libopus' restricted 20 ms framing.
func TestOSCELACEForwardPassRejectsBadInputs(t *testing.T) {
	blob := requireLibopusOSCELACEModelBlob(t)
	parsed, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	model, err := osceLACE.Load(parsed)
	if err != nil {
		t.Fatalf("osceLACE.Load: %v", err)
	}

	var state osceLACE.LACEState
	if err := state.SetModel(model); err != nil {
		t.Fatalf("SetModel: %v", err)
	}

	features := make([]float32, 4*93)
	numbits := []float32{200.0, 200.0}
	periods := []int{60, 60, 60, 60}

	var unloaded osceLACE.LACEState
	if err := unloaded.SetModel(&osceLACE.Model{}); err != nil {
		t.Fatalf("SetModel(zero model): %v", err)
	}
	if err := unloaded.Process(make([]float32, 320), make([]float32, 320), features, numbits, periods); err == nil {
		t.Fatalf("Process accepted an unloaded model; expected error")
	}

	// Wrong input length.
	in := make([]float32, 160)
	out := make([]float32, 320)
	if err := state.Process(in, out, features, numbits, periods); err == nil {
		t.Fatalf("Process accepted input of length 160; expected error")
	}

	// Too-short output buffer.
	in = make([]float32, 320)
	out = make([]float32, 100)
	if err := state.Process(in, out, features, numbits, periods); err == nil {
		t.Fatalf("Process accepted truncated output buffer; expected error")
	}

	// Wrong features length.
	in = make([]float32, 320)
	out = make([]float32, 320)
	if err := state.Process(in, out, features[:1], numbits, periods); err == nil {
		t.Fatalf("Process accepted features of wrong length; expected error")
	}

	// Wrong periods length.
	if err := state.Process(in, out, features, numbits, periods[:1]); err == nil {
		t.Fatalf("Process accepted periods of wrong length; expected error")
	}

	badPeriods := append([]int(nil), periods...)
	badPeriods[0] = 301
	if err := state.Process(in, out, features, numbits, badPeriods); err == nil {
		t.Fatalf("Process accepted out-of-range period; expected error")
	}

	// Wrong numbits length.
	if err := state.Process(in, out, features, numbits[:1], periods); err == nil {
		t.Fatalf("Process accepted numbits of wrong length; expected error")
	}
}

func TestOSCELACEModelLoadsFromLibopusBlob(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	laceBlob := requireLibopusOSCELACEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(laceBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, laceBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+LACE) error: %v", err)
	}
	if !dec.osceModelsLoaded {
		t.Fatalf("decoder did not retain osceModelsLoaded after SetDNNBlob")
	}
	if !dec.osceLACEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE LACE runtime model after SetDNNBlob")
	}

	// Re-applying with an OSCE-LACE-free blob must clear the runtime binding
	// while keeping the core neural models loaded.
	if err := dec.SetDNNBlob(coreBlob); err != nil {
		t.Fatalf("SetDNNBlob(core-only) error: %v", err)
	}
	if dec.osceModelsLoaded {
		t.Fatalf("decoder unexpectedly retained osceModelsLoaded after core-only blob")
	}
	if dec.osceLACEModelLoadedRuntime() {
		t.Fatalf("decoder unexpectedly retained OSCE LACE runtime binding after core-only blob")
	}
}

func TestDecoderOSCEFloatToInt16MatchesLibopusScaleOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   float32
		want int16
	}{
		{name: "positive clamp", in: 1.5, want: 32767},
		{name: "negative clamp", in: -1.5, want: -32767},
		{name: "negative full scale", in: -1.0, want: -32767},
		{name: "half tie to even", in: float32(0.5 / 32768.0), want: 0},
		{name: "one point five tie to even", in: float32(1.5 / 32768.0), want: 2},
		{name: "two point five tie to even", in: float32(2.5 / 32768.0), want: 2},
		{name: "negative one point five tie to even", in: float32(-1.5 / 32768.0), want: -2},
	} {
		if got := osceFloatToInt16(tc.in); got != tc.want {
			t.Fatalf("%s: osceFloatToInt16(%g)=%d want %d", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestDecoderOSCELACECrossFadeTransition exercises LACE state across
// SILK-containing packets. It verifies that:
//
//   - Each decode completes without error and returns the expected sample
//     count.
//   - The LACE-active state remains set across SILK WB and Hybrid frames,
//     while the Hybrid frame consumes the pending reset cross-fade.
//   - The PCM output contains no NaN/Inf samples and stays inside the
//     [-1.5, 1.5] envelope -- the cross-fade is a weighted sum of two
//     bounded signals so it cannot produce wild discontinuities.
//   - The cross-fade boundary at the start of the Hybrid frame does not
//     introduce a step discontinuity larger than the in-frame dynamic range.
func osceLACETransitionPackets(t *testing.T) [][]byte {
	t.Helper()
	const frameSize = 960
	return [][]byte{
		makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband),
		makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband),
		makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband),
		makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband),
	}
}

func newOSCELACETransitionTestDecoder(t *testing.T) *Decoder {
	t.Helper()
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	laceBlob := requireLibopusOSCELACEModelBlob(t)
	merged := make([]byte, 0, len(coreBlob)+len(laceBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, laceBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(mono 48kHz): %v", err)
	}
	if err := dec.SetComplexity(6); err != nil {
		t.Fatalf("SetComplexity(6): %v", err)
	}
	if err := dec.SetOSCELACE(true); err != nil {
		t.Fatalf("SetOSCELACE(true): %v", err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+LACE): %v", err)
	}
	if !dec.osceLACEModelLoadedRuntime() {
		t.Fatal("decoder did not bind OSCE LACE runtime model after SetDNNBlob")
	}
	return dec
}

func TestDecoderOSCELACECrossFadeTransition(t *testing.T) {
	const frameSize = 960 // 20 ms @ 48 kHz
	packets := osceLACETransitionPackets(t)
	silkWBA, hybridSWB, silkWBB, silkWBC := packets[0], packets[1], packets[2], packets[3]
	dec := newOSCELACETransitionTestDecoder(t)

	pcmA := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	pcmB := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	pcmC := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	pcmD := make([]float32, dec.maxPacketSamples*int(dec.Channels()))

	// Step 1: SILK WB -- LACE active. prevLACEActive transitions to true.
	// libopus keeps this first eligible frame raw after reset and leaves one
	// reset frame pending for the next SILK-containing frame.
	gotA, err := dec.Decode(silkWBA, pcmA)
	if err != nil {
		t.Fatalf("Decode(silk WB #1): %v", err)
	}
	if gotA != frameSize {
		t.Fatalf("Decode(silk WB #1) returned %d samples, want %d", gotA, frameSize)
	}
	if dec.osceLACE == nil || !dec.osceLACE.prevLACEActive {
		t.Fatalf("prevLACEActive=false after SILK WB decode (LACE should be active)")
	}
	if dec.osceLACE.laceResetFrames[0] != 1 {
		t.Fatalf("reset countdown after first SILK WB=%d want 1", dec.osceLACE.laceResetFrames[0])
	}

	// Step 2: Hybrid SWB still decodes a 16 kHz SILK low band. Its LACE
	// transition consumes the pending 10 ms cross-fade.
	gotB, err := dec.Decode(hybridSWB, pcmB)
	if err != nil {
		t.Fatalf("Decode(hybrid SWB): %v", err)
	}
	if gotB != frameSize {
		t.Fatalf("Decode(hybrid SWB) returned %d samples, want %d", gotB, frameSize)
	}
	if dec.osceLACE == nil || !dec.osceLACE.prevLACEActive {
		t.Fatal("Hybrid SWB did not retain active LACE state for its SILK low band")
	}
	if dec.osceLACE.laceResetFrames[0] != 0 {
		t.Fatalf("reset countdown after Hybrid SWB=%d want 0 after the LACE cross-fade", dec.osceLACE.laceResetFrames[0])
	}

	// Step 3: SILK WB again continues the same active LACE stream without
	// restarting the reset/cross-fade sequence.
	gotC, err := dec.Decode(silkWBB, pcmC)
	if err != nil {
		t.Fatalf("Decode(silk WB #2): %v", err)
	}
	if gotC != frameSize {
		t.Fatalf("Decode(silk WB #2) returned %d samples, want %d", gotC, frameSize)
	}
	if dec.osceLACE == nil || !dec.osceLACE.prevLACEActive {
		t.Fatalf("prevLACEActive=false after SILK WB transition (LACE should be active)")
	}
	if dec.osceLACE.laceResetFrames[0] != 0 {
		t.Fatalf("reset countdown after SILK continuation=%d want 0", dec.osceLACE.laceResetFrames[0])
	}

	// Step 4: consecutive SILK WB continues with LACE after the reset
	// cross-fade was consumed by the Hybrid frame.
	gotD, err := dec.Decode(silkWBC, pcmD)
	if err != nil {
		t.Fatalf("Decode(silk WB #3): %v", err)
	}
	if gotD != frameSize {
		t.Fatalf("Decode(silk WB #3) returned %d samples, want %d", gotD, frameSize)
	}
	if dec.osceLACE.laceResetFrames[0] != 0 {
		t.Fatalf("reset countdown after SILK continuation=%d want 0", dec.osceLACE.laceResetFrames[0])
	}

	checkPCMSane := func(t *testing.T, name string, pcm []float32, n int) {
		t.Helper()
		var maxAbs float32
		for i := 0; i < n; i++ {
			v := pcm[i]
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("%s: PCM contains NaN/Inf at sample %d: %v", name, i, v)
			}
			if v > maxAbs {
				maxAbs = v
			} else if -v > maxAbs {
				maxAbs = -v
			}
		}
		if maxAbs > 1.5 {
			t.Fatalf("%s: PCM exceeds [-1.5, 1.5] envelope (maxAbs=%v); cross-fade likely produced runaway samples", name, maxAbs)
		}
	}
	checkPCMSane(t, "silk WB #1", pcmA, gotA)
	checkPCMSane(t, "hybrid SWB", pcmB, gotB)
	checkPCMSane(t, "silk WB #2", pcmC, gotC)
	checkPCMSane(t, "silk WB #3", pcmD, gotD)

	// Sanity: the LACE cross-fade region (first 480 samples of the Hybrid
	// frame at 48 kHz, derived from the first 160 samples of the 16 kHz
	// native lowband which the silk_resampler upsamples) should be continuous.
	// The boundary step must not exceed the in-frame maximum.
	maxStep := func(pcm []float32, start, end int) float32 {
		var m float32
		for i := start + 1; i < end; i++ {
			d := pcm[i] - pcm[i-1]
			if d < 0 {
				d = -d
			}
			if d > m {
				m = d
			}
		}
		return m
	}
	xfadeStepB := maxStep(pcmB, 0, 480)
	fullStepB := maxStep(pcmB, 0, gotB)
	if xfadeStepB > fullStepB+1e-3 {
		t.Fatalf("Hybrid LACE cross-fade produced step %v exceeding in-frame max %v", xfadeStepB, fullStepB)
	}
}

// TestDecoderOSCELACEHybridTransitionMatchesSelectedLibopus checks the
// transition against the matching OSCE-enabled libopus build. The C
// silk/decode_frame.c success path calls osce_enhance_frame for Hybrid frames
// after decoding their 16 kHz SILK low band.
func TestDecoderOSCELACEHybridTransitionMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := getLibopusOSCEDecodeSingleHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE decode single", err)
	}

	const (
		sampleRate = 48000
		frameSize  = 960
	)
	packets := osceLACETransitionPackets(t)
	libopusPCM, err := runLibopusOSCEDecodeSingle(binPath, sampleRate, 1, frameSize, 6, false, packets)
	if err != nil {
		t.Fatalf("selected libopus OSCE decode (complexity 6): %v", err)
	}
	libopusWithoutLACE, err := runLibopusOSCEDecodeSingle(binPath, sampleRate, 1, frameSize, 5, false, packets)
	if err != nil {
		t.Fatalf("selected libopus OSCE decode (complexity 5): %v", err)
	}
	wantSamples := frameSize * len(packets)
	if len(libopusPCM) != wantSamples || len(libopusWithoutLACE) != wantSamples {
		t.Fatalf("selected libopus samples: LACE=%d NoLACE=%d want %d each", len(libopusPCM), len(libopusWithoutLACE), wantSamples)
	}

	const hybridFrame = 1
	changedHybridSamples := 0
	for i := hybridFrame * frameSize; i < (hybridFrame+1)*frameSize; i++ {
		if math.Float32bits(libopusPCM[i]) != math.Float32bits(libopusWithoutLACE[i]) {
			changedHybridSamples++
		}
	}
	if changedHybridSamples == 0 {
		t.Fatal("selected libopus LACE output did not change any Hybrid SWB samples")
	}
	t.Logf("selected libopus LACE changes %d/%d Hybrid SWB samples", changedHybridSamples, frameSize)

	dec := newOSCELACETransitionTestDecoder(t)
	pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	for frame, packet := range packets {
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode packet %d: %v", frame, err)
		}
		if got != frameSize {
			t.Fatalf("Decode packet %d returned %d samples, want %d", frame, got, frameSize)
		}
		for sample := 0; sample < frameSize; sample++ {
			index := frame*frameSize + sample
			if gotBits, wantBits := math.Float32bits(pcm[sample]), math.Float32bits(libopusPCM[index]); gotBits != wantBits {
				t.Fatalf("packet %d sample %d: Go=%08x selected libopus=%08x", frame, sample, gotBits, wantBits)
			}
		}
	}
}

// TestDecoderOSCELACEPLC verifies LACE/NoLACE follows libopus PLC semantics:
// the lost SILK frame resets postfilter state instead of running
// osce_enhance_frame. The PLC output must be non-zero and must not contain
// NaN/Inf samples.
func TestDecoderOSCELACEPLC(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	laceBlob := requireLibopusOSCELACEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(laceBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, laceBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(mono 48kHz): %v", err)
	}
	if err := dec.SetComplexity(6); err != nil {
		t.Fatalf("SetComplexity(6): %v", err)
	}
	if err := dec.SetOSCELACE(true); err != nil {
		t.Fatalf("SetOSCELACE(true): %v", err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+LACE): %v", err)
	}
	if !dec.osceLACEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE LACE runtime model after SetDNNBlob")
	}

	const frameSize = 960 // 20 ms @ 48 kHz
	silkWB := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

	// Step 1: decode a SILK WB packet so the decoder retains valid
	// lastPacketMode/lastBandwidth for the upcoming PLC.
	pcmGood := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	gotGood, err := dec.Decode(silkWB, pcmGood)
	if err != nil {
		t.Fatalf("Decode(silk WB): %v", err)
	}
	if gotGood != frameSize {
		t.Fatalf("Decode(silk WB) returned %d samples, want %d", gotGood, frameSize)
	}
	if dec.lastPacketMode != ModeSILK || dec.lastBandwidth != BandwidthWideband {
		t.Fatalf("decoder state after good SILK WB packet: mode=%v bandwidth=%v, want SILK WB", dec.lastPacketMode, dec.lastBandwidth)
	}

	// Step 2: invoke Decode(nil) for PLC. With LACE armed, the PLC path
	// must reset the postfilter state instead of enhancing the concealed
	// frame, matching libopus silk_decode_frame lost-branch behavior.
	pcmPLC := make([]float32, frameSize*dec.Channels())
	gotPLC, err := dec.Decode(nil, pcmPLC)
	if err != nil {
		t.Fatalf("Decode(nil) PLC: %v", err)
	}
	if gotPLC != frameSize {
		t.Fatalf("Decode(nil) PLC returned %d samples, want %d", gotPLC, frameSize)
	}

	if dec.osceLACE == nil || dec.osceLACE.laceResetFrames[0] != 2 || dec.osceLACE.laceMethod != osceLACEModeLACE {
		t.Fatal("SILK WB PLC must retain LACE and arm its two-frame output reset")
	}

	// PLC output must be non-zero -- the silk_resampler upsampling alone
	// already produces non-zero energy from the concealed lowband, so this
	// check guards against an accidental regression where the PLC path zeroes
	// out the buffer or otherwise yields silence.
	var energy float64
	for i := 0; i < gotPLC*dec.Channels(); i++ {
		v := pcmPLC[i]
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("PLC PCM contains NaN/Inf at sample %d: %v", i, v)
		}
		energy += float64(v) * float64(v)
	}
	if energy == 0 {
		t.Fatalf("PLC PCM is silent after LACE-armed SILK WB packet")
	}

	t.Run("stereo", func(t *testing.T) {
		dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(stereo 48kHz): %v", err)
		}
		if err := dec.SetComplexity(6); err != nil {
			t.Fatalf("SetComplexity(6): %v", err)
		}
		if err := dec.SetOSCELACE(true); err != nil {
			t.Fatalf("SetOSCELACE(true): %v", err)
		}
		if err := dec.SetDNNBlob(merged); err != nil {
			t.Fatalf("SetDNNBlob(merged core+LACE): %v", err)
		}
		if !dec.osceLACEModelLoadedRuntime() {
			t.Fatalf("decoder did not bind OSCE LACE runtime model after SetDNNBlob")
		}

		silkWB := makeValidStereoSILKPacketForFrameSizeBandwidthForOSCEBWETest(t, frameSize, BandwidthWideband)
		pcmGood := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
		gotGood, err := dec.Decode(silkWB, pcmGood)
		if err != nil {
			t.Fatalf("Decode(stereo silk WB): %v", err)
		}
		if gotGood != frameSize {
			t.Fatalf("Decode(stereo silk WB) returned %d samples, want %d", gotGood, frameSize)
		}
		if dec.lastPacketMode != ModeSILK || dec.lastBandwidth != BandwidthWideband || !dec.prevPacketStereo {
			t.Fatalf("decoder state after good stereo SILK WB packet: mode=%v bandwidth=%v stereo=%v, want SILK WB stereo", dec.lastPacketMode, dec.lastBandwidth, dec.prevPacketStereo)
		}
		if dec.osceLACE == nil || !dec.osceLACE.prevLACEActive {
			t.Fatalf("prevLACEActive=false after stereo SILK WB decode")
		}

		pcmPLC := make([]float32, frameSize*dec.Channels())
		gotPLC, err := dec.Decode(nil, pcmPLC)
		if err != nil {
			t.Fatalf("Decode(nil) stereo PLC: %v", err)
		}
		if gotPLC != frameSize {
			t.Fatalf("Decode(nil) stereo PLC returned %d samples, want %d", gotPLC, frameSize)
		}
		if dec.osceLACE == nil || dec.osceLACE.laceResetFrames != [2]int{2, 2} || dec.osceLACE.laceMethod != osceLACEModeLACE {
			t.Fatal("stereo SILK WB PLC must retain LACE and arm both channel resets")
		}

		var leftEnergy, rightEnergy, diffEnergy float64
		for i := 0; i < gotPLC; i++ {
			l := pcmPLC[2*i]
			r := pcmPLC[2*i+1]
			if math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) {
				t.Fatalf("stereo PLC left PCM contains NaN/Inf at sample %d: %v", i, l)
			}
			if math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) {
				t.Fatalf("stereo PLC right PCM contains NaN/Inf at sample %d: %v", i, r)
			}
			leftEnergy += float64(l) * float64(l)
			rightEnergy += float64(r) * float64(r)
			diff := float64(l - r)
			diffEnergy += diff * diff
		}
		if leftEnergy == 0 {
			t.Fatalf("stereo PLC left channel is silent after LACE-armed SILK WB packet")
		}
		if rightEnergy == 0 {
			t.Fatalf("stereo PLC right channel is silent after LACE-armed SILK WB packet")
		}
		if diffEnergy == 0 {
			t.Fatalf("stereo PLC collapsed to mono after LACE-armed stereo SILK WB packet")
		}
	})
}

func TestDecoderOSCELACEComplexityMode(t *testing.T) {
	for _, tc := range []struct {
		complexity int
		want       osceLACEMode
	}{
		{complexity: 5, want: osceLACEModeNone},
		{complexity: 6, want: osceLACEModeLACE},
		{complexity: 7, want: osceLACEModeNoLACE},
		{complexity: 10, want: osceLACEModeNoLACE},
	} {
		if got := pickOSCELACEMode(tc.complexity); got != tc.want {
			t.Fatalf("pickOSCELACEMode(%d)=%v want %v", tc.complexity, got, tc.want)
		}
	}
}

func TestDecoderOSCELACEGetterReportsEffectiveComplexityGate(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	for _, tc := range []struct {
		complexity int
		want       bool
	}{
		{complexity: 0, want: false},
		{complexity: 5, want: false},
		{complexity: 6, want: true},
		{complexity: 7, want: true},
	} {
		if err := dec.SetComplexity(tc.complexity); err != nil {
			t.Fatalf("SetComplexity(%d): %v", tc.complexity, err)
		}
		if got, err := dec.OSCELACE(); err != nil || got != tc.want {
			t.Fatalf("OSCELACE() at complexity %d=(%v,%v) want (%v,nil)", tc.complexity, got, err, tc.want)
		}
	}
	if err := dec.SetOSCELACE(false); err != nil {
		t.Fatalf("SetOSCELACE(false): %v", err)
	}
	if got, err := dec.OSCELACE(); err != nil || got {
		t.Fatalf("OSCELACE() after explicit false=(%v,%v) want (false,nil)", got, err)
	}
	if err := dec.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity(5): %v", err)
	}
	if err := dec.SetOSCELACE(true); err != nil {
		t.Fatalf("SetOSCELACE(true): %v", err)
	}
	if got, err := dec.OSCELACE(); err != nil || !got {
		t.Fatalf("OSCELACE() after explicit true=(%v,%v) want (true,nil)", got, err)
	}
}

func TestDecoderOSCELACERuntimeIntegration(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	laceBlob := requireLibopusOSCELACEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(laceBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, laceBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(mono 48kHz): %v", err)
	}
	if err := dec.SetComplexity(6); err != nil {
		t.Fatalf("SetComplexity(6): %v", err)
	}

	if err := dec.SetOSCELACE(true); err != nil {
		t.Fatalf("SetOSCELACE(true): %v", err)
	}
	enabled, err := dec.OSCELACE()
	if err != nil {
		t.Fatalf("OSCELACE(): %v", err)
	}
	if !enabled {
		t.Fatalf("OSCELACE() == false after SetOSCELACE(true)")
	}

	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+LACE): %v", err)
	}
	if !dec.osceModelsLoaded {
		t.Fatalf("decoder did not retain osceModelsLoaded after SetDNNBlob")
	}
	if !dec.osceLACEModelLoaded {
		t.Fatalf("decoder did not set osceLACEModelLoaded after SetDNNBlob")
	}
	if !dec.osceLACEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE LACE runtime model after SetDNNBlob")
	}

	// SILK WB packets satisfy the libopus `osce_enhance_frame` early-
	// return gate (fs_kHz == 16, nb_subfr == 4) so the postfilter hook is
	// expected to invoke the model-backed forward pass. The decoded PCM
	// must remain non-zero.
	t.Run("silk_wb_invokes_lace", func(t *testing.T) {
		dec.Reset()
		const frameSize = 960 // 20 ms @ 48 kHz
		packet := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
			t.Fatalf("unexpected TOC: mode=%v bandwidth=%v", toc.Mode, toc.Bandwidth)
		}

		pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(silk WB packet): %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode(silk WB packet) returned %d samples per channel, want %d", got, frameSize)
		}

		var energy float64
		for _, v := range pcm[:got*dec.Channels()] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("decoded PCM contains NaN/Inf: %v", v)
			}
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("decoded PCM is silent after LACE-armed SILK WB decode -- standard silk_resampler output was unexpectedly clobbered")
		}
	})

	// Hybrid SWB packets do NOT satisfy the libopus LACE/NoLACE gate
	// (mode != MODE_SILK_ONLY); the postfilter helper must short-circuit
	// and leave the standard hybrid upsampler output untouched.
	t.Run("hybrid_swb_bypasses_lace", func(t *testing.T) {
		dec.Reset()
		const frameSize = 960 // 20 ms @ 48 kHz
		packet := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeHybrid || toc.Bandwidth != BandwidthSuperwideband {
			t.Fatalf("unexpected TOC: mode=%v bandwidth=%v", toc.Mode, toc.Bandwidth)
		}

		pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(hybrid SWB packet): %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode(hybrid SWB packet) returned %d samples per channel, want %d", got, frameSize)
		}

		var energy float64
		for _, v := range pcm[:got*dec.Channels()] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("decoded PCM contains NaN/Inf: %v", v)
			}
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("decoded PCM is silent after LACE-armed Hybrid SWB decode -- standard hybrid output was unexpectedly clobbered by the postfilter bypass branch")
		}
	})

	// libopus reset semantics keep the first eligible LACE frame raw, then
	// cross-fade the second eligible frame before steady enhancement.
	// The current call site still runs after silk_resampler, so this checks
	// the native lowband that downstream OSCE BWE consumes.
	t.Run("silk_wb_lace_reset_then_mutates_native_lowband", func(t *testing.T) {
		const frameSize = 960 // 20 ms @ 48 kHz
		packetA := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
		packetB := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

		decRef, err := NewDecoder(DefaultDecoderConfig(48000, 1))
		if err != nil {
			t.Fatalf("NewDecoder(ref): %v", err)
		}
		if err := decRef.SetOSCELACE(false); err != nil {
			t.Fatalf("SetOSCELACE(false): %v", err)
		}
		if err := decRef.SetDNNBlob(merged); err != nil {
			t.Fatalf("SetDNNBlob(ref): %v", err)
		}
		pcmRef := make([]float32, decRef.maxPacketSamples*int(decRef.channels))
		if _, err := decRef.Decode(packetA, pcmRef); err != nil {
			t.Fatalf("Decode(ref #1): %v", err)
		}
		pcmRefFirst := append([]float32(nil), pcmRef[:frameSize]...)
		nativeRef, fsKHzRef := decRef.silkDecoder.LatestNativeMono()
		if nativeRef == nil || fsKHzRef != 16 {
			t.Fatalf("ref decode produced no 16 kHz native lowband: nativeRef=%v fsKHz=%d", nativeRef, fsKHzRef)
		}
		nativeRefFirst := make([]int16, osceLACEFrameSamples)
		copy(nativeRefFirst, nativeRef[:osceLACEFrameSamples])
		if _, err := decRef.Decode(packetB, pcmRef); err != nil {
			t.Fatalf("Decode(ref #2): %v", err)
		}
		nativeRef, fsKHzRef = decRef.silkDecoder.LatestNativeMono()
		if nativeRef == nil || fsKHzRef != 16 {
			t.Fatalf("ref decode #2 produced no 16 kHz native lowband: nativeRef=%v fsKHz=%d", nativeRef, fsKHzRef)
		}
		nativeRefSecond := make([]int16, osceLACEFrameSamples)
		copy(nativeRefSecond, nativeRef[:osceLACEFrameSamples])

		decLACE, err := NewDecoder(DefaultDecoderConfig(48000, 1))
		if err != nil {
			t.Fatalf("NewDecoder(lace): %v", err)
		}
		if err := decLACE.SetComplexity(6); err != nil {
			t.Fatalf("SetComplexity(6): %v", err)
		}
		if err := decLACE.SetOSCELACE(true); err != nil {
			t.Fatalf("SetOSCELACE(true): %v", err)
		}
		if err := decLACE.SetDNNBlob(merged); err != nil {
			t.Fatalf("SetDNNBlob(lace): %v", err)
		}
		pcmLACE := make([]float32, decLACE.maxPacketSamples*int(decLACE.channels))
		if _, err := decLACE.Decode(packetA, pcmLACE); err != nil {
			t.Fatalf("Decode(lace #1): %v", err)
		}
		publicDiffCount, publicMaxDiff := float32BufferDiff(pcmLACE[:frameSize], pcmRefFirst)
		if publicDiffCount != 0 {
			t.Fatalf("first LACE-eligible public PCM differs from raw output after reset: diffCount=%d maxDiff=%g", publicDiffCount, publicMaxDiff)
		}
		nativeLACE, fsKHzLACE := decLACE.silkDecoder.LatestNativeMono()
		if nativeLACE == nil || fsKHzLACE != 16 {
			t.Fatalf("lace decode produced no 16 kHz native lowband: nativeLACE=%v fsKHz=%d", nativeLACE, fsKHzLACE)
		}
		diffCount, maxDiff := int16BufferDiff(nativeLACE[:osceLACEFrameSamples], nativeRefFirst)
		if diffCount != 0 {
			t.Fatalf("first LACE-eligible frame differs from raw output after reset: diffCount=%d maxDiff=%d", diffCount, maxDiff)
		}
		if decLACE.osceLACE == nil {
			t.Fatalf("LACE state is nil after first eligible frame")
		}
		if decLACE.osceLACE.laceResetFrames[0] != 1 {
			t.Fatalf("after first LACE frame reset countdown=%d, want 1", decLACE.osceLACE.laceResetFrames[0])
		}

		if _, err := decLACE.Decode(packetB, pcmLACE); err != nil {
			t.Fatalf("Decode(lace #2): %v", err)
		}
		nativeLACE, fsKHzLACE = decLACE.silkDecoder.LatestNativeMono()
		if nativeLACE == nil || fsKHzLACE != 16 {
			t.Fatalf("lace decode #2 produced no 16 kHz native lowband: nativeLACE=%v fsKHz=%d", nativeLACE, fsKHzLACE)
		}
		diffCount, maxDiff = int16BufferDiff(nativeLACE[:osceLACEFrameSamples], nativeRefSecond)
		if diffCount == 0 {
			t.Fatalf("second LACE-eligible frame is still raw: forward pass/reset cross-fade did not affect native lowband")
		}
		publicDiffCount, publicMaxDiff = float32BufferDiff(pcmLACE[:frameSize], pcmRef[:frameSize])
		if publicDiffCount == 0 {
			t.Fatalf("second LACE-eligible public PCM is still raw: native postfilter did not feed silk_resampler")
		}
		secondNativeDiff, secondNativeMax := diffCount, maxDiff
		secondPublicDiff, secondPublicMax := publicDiffCount, publicMaxDiff
		if decLACE.osceLACE.laceResetFrames[0] != 0 {
			t.Fatalf("after second LACE frame reset countdown=%d, want 0", decLACE.osceLACE.laceResetFrames[0])
		}
		decLACE.Reset()
		if _, err := decLACE.Decode(packetA, pcmLACE); err != nil {
			t.Fatalf("Decode(lace after Reset): %v", err)
		}
		publicDiffCount, publicMaxDiff = float32BufferDiff(pcmLACE[:frameSize], pcmRefFirst)
		if publicDiffCount != 0 {
			t.Fatalf("post-Reset LACE public PCM differs from raw output: diffCount=%d maxDiff=%g", publicDiffCount, publicMaxDiff)
		}
		nativeLACE, fsKHzLACE = decLACE.silkDecoder.LatestNativeMono()
		if nativeLACE == nil || fsKHzLACE != 16 {
			t.Fatalf("lace post-Reset decode produced no 16 kHz native lowband: nativeLACE=%v fsKHz=%d", nativeLACE, fsKHzLACE)
		}
		diffCount, maxDiff = int16BufferDiff(nativeLACE[:osceLACEFrameSamples], nativeRefFirst)
		if diffCount != 0 {
			t.Fatalf("post-Reset LACE native frame differs from raw output: diffCount=%d maxDiff=%d", diffCount, maxDiff)
		}
		if decLACE.osceLACE.laceResetFrames[0] != 1 {
			t.Fatalf("after post-Reset LACE frame reset countdown=%d, want 1", decLACE.osceLACE.laceResetFrames[0])
		}
		t.Logf("LACE postfilter altered %d/%d native int16 samples (max %d) and %d/%d public PCM samples (max %g)",
			secondNativeDiff, osceLACEFrameSamples, secondNativeMax, secondPublicDiff, frameSize, secondPublicMax)
	})

	// SetOSCELACE(false) disables the helper at runtime; the SILK WB
	// decode must still produce non-zero audio with the postfilter off.
	t.Run("disable_keeps_decode_working", func(t *testing.T) {
		if err := dec.SetOSCELACE(false); err != nil {
			t.Fatalf("SetOSCELACE(false): %v", err)
		}
		enabled, err := dec.OSCELACE()
		if err != nil {
			t.Fatalf("OSCELACE(): %v", err)
		}
		if enabled {
			t.Fatalf("OSCELACE() == true after SetOSCELACE(false)")
		}

		dec.Reset()
		const frameSize = 960
		packet := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
		pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(silk WB packet) after disable: %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode returned %d samples, want %d", got, frameSize)
		}
		var energy float64
		for _, v := range pcm[:got*dec.Channels()] {
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("decoded PCM silent with LACE disabled")
		}
	})
}

func int16BufferDiff(a, b []int16) (count, maxAbs int) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		if d > 0 {
			count++
			if d > maxAbs {
				maxAbs = d
			}
		}
	}
	count += len(a) - n
	count += len(b) - n
	return count, maxAbs
}

func float32BufferDiff(a, b []float32) (count int, maxAbs float32) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		d := a[i] - b[i]
		if d < 0 {
			d = -d
		}
		if d > 0 {
			count++
			if d > maxAbs {
				maxAbs = d
			}
		}
	}
	count += len(a) - n
	count += len(b) - n
	return count, maxAbs
}
