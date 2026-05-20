//go:build gopus_extra_controls
// +build gopus_extra_controls

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
)

// TestOSCELACEForwardPassSmoke is the Phase 2a structural smoke test for the
// OSCE LACE/NoLACE postfilter forward pass. It loads the libopus LACE blob,
// drives a simple sinusoidal lowband input through both LACE and NoLACE and
// asserts:
//
//  1. The runtimes accept the libopus blob and report Loaded()==true.
//  2. The 20 ms input (320 samples @ 16 kHz) produces exactly 320 samples
//     (LACE/NoLACE are postfilters, not upsamplers).
//  3. Outputs are finite (no NaN/Inf) and bounded.
//  4. The output carries non-zero energy when fed a non-zero input.
//
// True sample-level parity with libopus is deferred to a later phase; this
// test uses zero features so the runtime is exercised end-to-end with a
// well-defined input independent of the (not-yet-ported) OSCE feature
// extractor.
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
