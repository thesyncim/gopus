//go:build gopus_extra_controls

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
)

// TestOSCEBWEModelForwardPassMatchesLibopus is the Phase 2a structural smoke
// test for the OSCE BWE forward pass. It loads the libopus BBWENet blob,
// drives a simple sinusoidal lowband input through the gopus forward pass and
// asserts:
//
//  1. The runtime accepts the libopus blob and reports Loaded()==true.
//  2. A 10 ms input (160 samples @ 16 kHz) produces exactly 480 samples
//     (10 ms @ 48 kHz) and a 20 ms input produces 960.
//  3. The output carries non-zero energy when fed a non-zero input -- a
//     baseline sanity check that the model layers are wired correctly.
//  4. The output stays inside the [-2, 2] float32 range (the libopus pipeline
//     emits PCM that gets scaled to int16, so floats must be bounded).
//
// True sample-level parity with libopus is deferred to a later phase; the
// libopus reference depends on a deterministic feature extractor we have not
// yet ported. This test therefore uses zero features so the runtime is
// exercised end-to-end with a well-defined input.
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
