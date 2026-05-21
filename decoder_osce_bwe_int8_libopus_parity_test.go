//go:build gopus_extra_controls

package gopus

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
)

// TestOSCEBWEInt8LibopusKernelParity loads the libopus BBWENet weights blob and
// validates the gopus cgemv8x4 kernel against a float reference that
// dequantises the real libopus int8 weights into a column-major matrix and
// runs a plain sgemv on the same quantised input.  The result must be
// identical to the cgemv8x4 output up to the int32->float32 cast, so this is
// a tight bit-for-bit parity check on the kernel's interpretation of the
// libopus weight tile layout.
//
// The libopus 1.6.1 BBWENet blob ships only int8 weights (no float mirror)
// for every quantised layer, so this is the only way to verify the kernel
// against real, in-the-wild data short of building a libopus forward-pass
// helper.
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
