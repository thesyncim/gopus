//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
)

// TestOSCEBWEForwardPassMatchesLibopusNumericalParity is the sentinel parity probe
// for the gopus OSCE BWE (BBWENet) forward pass. It builds the libopus
// reference helper (`tools/csrc/libopus_osce_bwe_forward.c`) against an
// OSCE-enabled libopus 1.6.1 build (`--enable-osce --enable-osce-bwe`),
// drives both libopus and gopus on the same deterministic 1 kHz 16 kHz
// sinusoid, and compares their 48 kHz outputs.
//
// Comparator status:
//
//		The pure-Go runtime in `internal/osce/bwe` now uses the libopus DNN
//		activation / exponential approximations plus the CELT log/sin helpers
//		used by the BWE Valin and AdaShape paths.
//
//		Additionally, libopus quantises the BBWENet float output to int16
//		with a 21-sample delay buffer in `osce_bwe(...)` before returning.
//		The gopus `State.ProcessDelayed` path mirrors that public wrapper while
//		`State.Process` remains available for raw BBWENet math parity.
//
//	  - Both pipelines accept the same shapes (160 / 320 samples) and emit
//	    the expected number of output samples (480 / 960).
//	  - The libopus-computed feature vectors and the gopus-computed feature
//	    vectors agree to within `featureTolerance` per element. (The feature
//	    extractor port is independent of the math-approximation issues
//	    above and is therefore the tighter numerical path.)
//	  - When the gopus forward pass is fed the libopus-computed features
//	    (so feature-extractor drift is eliminated), the delayed/int16-wrapper
//	    output is exact for 10 ms and within a ratcheted one-LSB numerical
//	    envelope for 20 ms.
//
// TestOSCEBWERawSignalNetMatchesLibopus separately exercises the raw BBWENet
// float path and keeps the signal-net math tolerance near float32 roundoff.
func TestOSCEBWEForwardPassMatchesLibopusNumericalParity(t *testing.T) {
	binPath, err := getLibopusOSCEBWEForwardHelperPath()
	if err != nil {
		t.Skipf("libopus OSCE BWE forward helper unavailable: %v", err)
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
		// 20 ms still has one int16 threshold crossing in the delayed wrapper.
		{"20ms", 320, 2, 3.1e-5, 1.2e-6},
	}

	const (
		featureTolerance   = 6e-7
		instafreqTolerance = 1.5e-7
	)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refFeatures, refOut, err := runOSCEBWEForwardHelper(binPath, tc.numIn16)
			if err != nil {
				t.Skipf("libopus OSCE BWE forward helper run failed: %v", err)
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

			// Compare features (within tolerance). We track the maximum
			// per-element error inside the lmspec block (first 32 floats
			// of each 114-vector) and the instafreq block (remaining 82)
			// separately because the instafreq and lmspec paths have
			// different sources of residual float drift.
			maxFeatErrLM := float32(0)
			maxIdxLM := -1
			maxFeatErrIF := float32(0)
			maxIdxIF := -1
			for i := range gopusFeatures {
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
	binPath, err := getLibopusOSCEBWEForwardHelperPath()
	if err != nil {
		t.Skipf("libopus OSCE BWE forward helper unavailable: %v", err)
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
				t.Skipf("libopus OSCE BWE raw helper failed: %v", err)
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
					t.Skipf("libopus OSCE BWE first-frame raw helper failed: %v", err)
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
// during PLC; the parity contract is therefore that the gopus second-frame
// output stays within the same numerical comparator envelope as the single-
// frame forward pass.
func TestOSCEBWEForwardPassPLCContinuityMatchesLibopus(t *testing.T) {
	binPath, err := getLibopusOSCEBWEForwardHelperPath()
	if err != nil {
		t.Skipf("libopus OSCE BWE forward helper unavailable: %v", err)
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
		t.Skipf("libopus OSCE BWE consecutive helper failed: %v", err)
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
		t.Skipf("libopus OSCE BWE forward helper failed: %v", err)
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
	binPath, err := getLibopusOSCEBWEForwardHelperPath()
	if err != nil {
		t.Skipf("libopus OSCE BWE forward helper unavailable: %v", err)
	}

	const numIn16 = 160
	_, refOut, err := runOSCEBWEForwardHelperMode(binPath, numIn16, "crossfade")
	if err != nil {
		t.Skipf("libopus OSCE BWE crossfade helper failed: %v", err)
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

	const (
		crossfadeAbsTolerance = float32(0)
		crossfadeRMSTolerance = float64(0)
	)
	var maxAbsErr float32
	var sumSq float64
	for i := 0; i < 480; i++ {
		d := fadeinF[i] - refOut[i]
		ad := d
		if ad < 0 {
			ad = -ad
		}
		if ad > maxAbsErr {
			maxAbsErr = ad
		}
		sumSq += float64(d) * float64(d)
	}
	rms := math.Sqrt(sumSq / 480)
	t.Logf("OSCE BWE crossfade parity: maxAbs=%g rms=%g (tolerances: maxAbs<=%g rms<=%g)",
		maxAbsErr, rms, crossfadeAbsTolerance, crossfadeRMSTolerance)
	if maxAbsErr > crossfadeAbsTolerance {
		t.Errorf("OSCE BWE crossfade max-abs error %g exceeds %g", maxAbsErr, crossfadeAbsTolerance)
	}
	if rms > crossfadeRMSTolerance {
		t.Errorf("OSCE BWE crossfade rms error %g exceeds %g", rms, crossfadeRMSTolerance)
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

var (
	libopusOSCEBWEForwardHelperOnce sync.Once
	libopusOSCEBWEForwardHelperPath string
	libopusOSCEBWEForwardHelperErr  error
)

// getLibopusOSCEBWEForwardHelperPath lazily builds (against the OSCE-enabled
// libopus build) the C reference helper `libopus_osce_bwe_forward.c` and
// caches the binary path for the lifetime of the test process.
func getLibopusOSCEBWEForwardHelperPath() (string, error) {
	libopusOSCEBWEForwardHelperOnce.Do(func() {
		libopusOSCEBWEForwardHelperPath, libopusOSCEBWEForwardHelperErr = buildLibopusOSCEHelper(
			"libopus_osce_bwe_forward.c",
			"gopus_libopus_osce_bwe_forward",
			true,
		)
	})
	if libopusOSCEBWEForwardHelperErr != nil {
		return "", libopusOSCEBWEForwardHelperErr
	}
	return libopusOSCEBWEForwardHelperPath, nil
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
	cmd := exec.Command(binPath, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("run libopus OSCE BWE forward helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	payload := stdout.Bytes()
	const tagLen = 8
	if len(payload) < tagLen+3*4 {
		return nil, nil, fmt.Errorf("libopus OSCE BWE forward output too short: %d bytes", len(payload))
	}
	if string(payload[:tagLen]) != "OSCEBWE\x00" {
		return nil, nil, fmt.Errorf("libopus OSCE BWE forward output missing tag, got %q", payload[:tagLen])
	}
	off := tagLen
	numFrames := int(int32(binary.LittleEndian.Uint32(payload[off:])))
	off += 4
	_ = int(int32(binary.LittleEndian.Uint32(payload[off:]))) // num_subframes (not used directly here)
	off += 4
	numOut := int(int32(binary.LittleEndian.Uint32(payload[off:])))
	off += 4

	featBytes := numFrames * osceBWE.FeatureDim * 4
	outBytes := numOut * 4
	if len(payload)-off < featBytes+outBytes {
		return nil, nil, fmt.Errorf("libopus OSCE BWE forward output truncated: have %d bytes for %d features + %d output", len(payload)-off, featBytes, outBytes)
	}
	features = make([]float32, numFrames*osceBWE.FeatureDim)
	for i := range features {
		features[i] = math.Float32frombits(binary.LittleEndian.Uint32(payload[off+4*i:]))
	}
	off += featBytes
	out48k = make([]float32, numOut)
	for i := range out48k {
		out48k[i] = math.Float32frombits(binary.LittleEndian.Uint32(payload[off+4*i:]))
	}
	return features, out48k, nil
}
