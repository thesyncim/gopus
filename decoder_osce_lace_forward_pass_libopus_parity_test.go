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
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
)

// TestOSCELACEForwardPassMatchesLibopus is the bounded-divergence parity
// probe for the gopus OSCE LACE / NoLACE postfilter forward pass.
//
// It mirrors the OSCE BWE parity test pattern (see
// `TestOSCEBWEForwardPassMatchesLibopusBitExact`): the libopus reference
// helper (`tools/csrc/libopus_osce_lace_forward.c`) is built against an
// OSCE-enabled libopus 1.6.1 build (`--enable-osce`) and the helper drives
// the static `lace_process_20ms_frame` / `nolace_process_20ms_frame`
// entries directly on a deterministic 1 kHz 16 kHz sinusoid. The gopus
// runtime is driven on the same input + same zero-features / zero-numbits /
// small-period inputs and the two 16 kHz 320-sample outputs are compared.
//
// Parity is bounded-divergence (not bit-exact) for the same reason the BWE
// probe is: libopus uses bespoke math approximations (`tansig_approx`,
// `sigmoid_approx`, `celt_exp`, `celt_log2`) that compound through the
// GRU + AdaConv + AdaComb stack. The shared DNN EXP approximation is now
// libopus-shaped, but LACE still carries residual math/order differences
// outside that seam. Per-mode tolerances are wider than the BWE bound (which
// has a shallower signal-net stack); see `cases` below for the active envelope.
func TestOSCELACEForwardPassMatchesLibopus(t *testing.T) {
	binPath, err := getLibopusOSCELACEForwardHelperPath()
	if err != nil {
		t.Skipf("libopus OSCE LACE forward helper unavailable: %v", err)
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

	// Per-mode tolerances. LACE has a 3-stage signal path (CF1 -> CF2 -> AF1);
	// NoLACE has a 5-stage signal path plus 3 AdaShape branches feeding GRU
	// hidden state. The math-approx drift compounds proportionally, so the
	// NoLACE bound is wider than LACE's. Both envelopes are conservatively
	// sized (~1.5x the currently-observed max-abs drift) so genuine regressions
	// in the math-approx layer or per-stage state continuity show up as test
	// failures while normal scalar/Generic-arch divergence is absorbed.
	cases := []struct {
		name               string
		mode               string
		outputAbsTolerance float32
		outputRMSTolerance float64
	}{
		{"LACE", "lace", 0.30, 0.15},
		{"NoLACE", "nolace", 0.50, 0.20},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refOut, err := runOSCELACEForwardHelper(binPath, inputLen, tc.mode)
			if err != nil {
				t.Skipf("libopus OSCE %s forward helper run failed: %v", tc.mode, err)
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
			var sumSq float64
			for i := 0; i < inputLen; i++ {
				d := out[i] - refOut[i]
				ad := d
				if ad < 0 {
					ad = -ad
				}
				if ad > maxAbsErr {
					maxAbsErr = ad
				}
				sumSq += float64(d) * float64(d)
			}
			rms := math.Sqrt(sumSq / float64(inputLen))
			t.Logf("OSCE %s forward-pass parity: maxAbs=%g rms=%g (tolerances: maxAbs<=%g rms<=%g)",
				tc.name, maxAbsErr, rms, tc.outputAbsTolerance, tc.outputRMSTolerance)
			if maxAbsErr > tc.outputAbsTolerance {
				t.Errorf("OSCE %s forward-pass max-abs error %g exceeds %g (signal-net divergence beyond bounded contract)",
					tc.name, maxAbsErr, tc.outputAbsTolerance)
			}
			if rms > tc.outputRMSTolerance {
				t.Errorf("OSCE %s forward-pass rms error %g exceeds %g (signal-net divergence beyond bounded contract)",
					tc.name, rms, tc.outputRMSTolerance)
			}
		})
	}
}

var (
	libopusOSCELACEForwardHelperOnce sync.Once
	libopusOSCELACEForwardHelperPath string
	libopusOSCELACEForwardHelperErr  error
)

// getLibopusOSCELACEForwardHelperPath lazily builds (against the OSCE-enabled
// libopus build) the C reference helper `libopus_osce_lace_forward.c` and
// caches the binary path for the lifetime of the test process.
func getLibopusOSCELACEForwardHelperPath() (string, error) {
	libopusOSCELACEForwardHelperOnce.Do(func() {
		libopusOSCELACEForwardHelperPath, libopusOSCELACEForwardHelperErr = buildLibopusOSCEHelper(
			"libopus_osce_lace_forward.c",
			"gopus_libopus_osce_lace_forward",
			true,
		)
	})
	if libopusOSCELACEForwardHelperErr != nil {
		return "", libopusOSCELACEForwardHelperErr
	}
	return libopusOSCELACEForwardHelperPath, nil
}

// runOSCELACEForwardHelper invokes the libopus OSCE LACE/NoLACE forward
// helper for `numIn16` samples (must be 320) in the requested mode ("lace"
// or "nolace"), parses the binary payload, and returns the libopus 16 kHz
// float output.
func runOSCELACEForwardHelper(binPath string, numIn16 int, mode string) (out16k []float32, err error) {
	cmd := exec.Command(binPath, fmt.Sprintf("%d", numIn16))
	cmd.Env = append(cmd.Env, fmt.Sprintf("MODE=%s", mode))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run libopus OSCE LACE forward helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	payload := stdout.Bytes()
	const tagLen = 8
	if len(payload) < tagLen+2*4 {
		return nil, fmt.Errorf("libopus OSCE LACE forward output too short: %d bytes", len(payload))
	}
	if string(payload[:tagLen]) != "OSCELAC\x00" {
		return nil, fmt.Errorf("libopus OSCE LACE forward output missing tag, got %q", payload[:tagLen])
	}
	off := tagLen
	_ = int(int32(binary.LittleEndian.Uint32(payload[off:]))) // mode_id (informational)
	off += 4
	numOut := int(int32(binary.LittleEndian.Uint32(payload[off:])))
	off += 4
	if numOut != numIn16 {
		return nil, fmt.Errorf("libopus OSCE LACE forward output: num_out=%d != num_in=%d", numOut, numIn16)
	}

	outBytes := numOut * 4
	if len(payload)-off < outBytes {
		return nil, fmt.Errorf("libopus OSCE LACE forward output truncated: have %d bytes for %d samples", len(payload)-off, numOut)
	}
	out16k = make([]float32, numOut)
	for i := range out16k {
		out16k[i] = math.Float32frombits(binary.LittleEndian.Uint32(payload[off+4*i:]))
	}
	return out16k, nil
}
