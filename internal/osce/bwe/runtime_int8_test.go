package bwe

import (
	"encoding/binary"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dnnmath"
)

// TestCGEMV8x4MatchesFloatReference exercises the cgemv8x4 kernel against a
// float reference that computes the same product by:
//
//  1. dequantising the int8 weight matrix (in 8x4 tile layout) into a
//     column-major float matrix with `scale[row]` already baked into each
//     coefficient, and
//  2. quantising the input the same way cgemv8x4 does for the active
//     libopus vector/scalar path so the dot products see identical operands.
//
// The integer accumulation must exactly match the float-accumulation form
// (modulo float-precision conversion of the int32 accumulator into float32);
// the int8 result is regarded as the ground truth and the float reference is
// allowed a tiny epsilon to absorb the int32 -> float32 cast.
func TestCGEMV8x4MatchesFloatReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xBBEED10))

	// Cover every (rows, cols) shape used by BBWENet int8 layers.
	cases := []struct {
		rows, cols int
	}{
		{128, 384}, // fnet_conv2
		{384, 128}, // fnet_gru_input / fnet_gru_recurrent
		{256, 128}, // fnet_tconv
		{80, 256},  // tdshape1_alpha1_f
		{120, 256}, // tdshape2_alpha1_f
		{48, 128},  // af1_kernel / af3_kernel
		{288, 128}, // af2_kernel
	}

	for _, tc := range cases {
		t.Run("shape", func(t *testing.T) {
			weights, scale := makeRandomInt8LayerData(rng, tc.rows, tc.cols)
			weightsView := mustInt8View(t, weights)
			scaleView := mustFloat32View(t, scale)

			// Random input bounded to [-1, 1] so the int8 quantisation is
			// well-defined (out-of-range inputs would saturate).
			in := make([]float32, tc.cols)
			for i := range in {
				in[i] = float32(rng.Float64()*2 - 1)
			}

			got := make([]float32, tc.rows)
			cgemv8x4(got, weightsView, scaleView, tc.rows, tc.cols, in)

			ref := referenceCGEMV8x4(weights, scale, tc.rows, tc.cols, in)

			for i := 0; i < tc.rows; i++ {
				if !approxEqual(got[i], ref[i], 1e-3) {
					t.Fatalf("row %d: got %.6f want %.6f (shape %dx%d)",
						i, got[i], ref[i], tc.rows, tc.cols)
				}
			}
		})
	}
}

// TestComputeLinearInt8MatchesDequantisedFloat verifies that the LinearLayer
// dispatcher routes int8 quantised weights through cgemv8x4 and that the
// resulting layer output stays within a tight tolerance of the equivalent
// "dequantise then sgemv" path. This is the runtime-level analogue of the
// kernel-level TestCGEMV8x4MatchesFloatReference test: it catches future
// regressions where the runtime stops using the int8 kernel or where the
// dispatcher silently falls back to a different code path.
func TestComputeLinearInt8MatchesDequantisedFloat(t *testing.T) {
	rng := rand.New(rand.NewSource(0xC0FFEE))

	const rows, cols = 256, 128
	weights, scale := makeRandomInt8LayerData(rng, rows, cols)
	bias := make([]float32, rows)
	for i := range bias {
		bias[i] = float32(rng.Float64()*0.2 - 0.1)
	}

	weightsView := mustInt8View(t, weights)
	scaleView := mustFloat32View(t, scale)
	biasView := mustFloat32View(t, bias)

	// Int8 layer.
	int8Layer := &LinearLayer{
		Bias:      biasView,
		Weights:   weightsView,
		Scale:     scaleView,
		NbInputs:  cols,
		NbOutputs: rows,
	}

	in := make([]float32, cols)
	for i := range in {
		in[i] = float32(rng.Float64()*2 - 1)
	}

	got := make([]float32, rows)
	computeLinear(int8Layer, got, in)

	// Reference: emulate libopus' int8 path exactly using the same
	// quantisation + tile layout. This is the same calculation, just
	// expressed as a float-only function so the parity check is
	// independent of the integer accumulator implementation.
	ref := referenceCGEMV8x4(weights, scale, rows, cols, in)
	for i := 0; i < rows; i++ {
		ref[i] += bias[i]
	}

	for i := 0; i < rows; i++ {
		if !approxEqual(got[i], ref[i], 1e-3) {
			t.Fatalf("row %d: got %.6f want %.6f", i, got[i], ref[i])
		}
	}
}

// referenceCGEMV8x4 is a float-only re-implementation of the cgemv8x4 scalar
// fallback in libopus dnn/vec.h. It performs the same integer-domain dot
// products in floating point so a round-trip parity check can compare two
// independent code paths.
func referenceCGEMV8x4(weights []int8, scale []float32, rows, cols int, x []float32) []float32 {
	q := make([]int32, cols)
	for i := 0; i < cols; i++ {
		q[i] = int32(dnnmath.Cgemv8x4QuantizeInput(x[i]))
	}
	out := make([]float32, rows)
	wOffset := 0
	for row := 0; row < rows; row += 8 {
		var acc [8]int32
		for col := 0; col < cols; col += 4 {
			x0 := q[col]
			x1 := q[col+1]
			x2 := q[col+2]
			x3 := q[col+3]
			for r := 0; r < 8; r++ {
				base := wOffset + r*4
				acc[r] += int32(weights[base])*x0 +
					int32(weights[base+1])*x1 +
					int32(weights[base+2])*x2 +
					int32(weights[base+3])*x3
			}
			wOffset += 32
		}
		for r := 0; r < 8; r++ {
			out[row+r] = float32(acc[r]) * scale[row+r]
		}
	}
	return out
}

// makeRandomInt8LayerData fabricates an int8 weight tile buffer plus matching
// per-row scale factors. Weights are uniformly distributed over the full int8
// range so the dot products exercise the full dynamic range of the kernel.
func makeRandomInt8LayerData(rng *rand.Rand, rows, cols int) ([]int8, []float32) {
	if rows%8 != 0 || cols%4 != 0 {
		panic("rows must be multiple of 8 and cols multiple of 4")
	}
	weights := make([]int8, rows*cols)
	for i := range weights {
		weights[i] = int8(rng.Intn(256) - 128)
	}
	scale := make([]float32, rows)
	for i := range scale {
		// Scale magnitudes mirror those observed in the libopus BBWENet
		// blob (roughly 1e-3 .. 1e-2 per row).
		scale[i] = float32((rng.Float64()*9 + 1) * 1e-3)
	}
	return weights, scale
}

func mustInt8View(t *testing.T, data []int8) dnnblob.Int8View {
	t.Helper()
	bytes := make([]byte, len(data))
	for i, v := range data {
		bytes[i] = byte(v)
	}
	view, err := dnnblob.Int8ViewFromBytes(bytes, int32(len(bytes)))
	if err != nil {
		t.Fatalf("Int8ViewFromBytes: %v", err)
	}
	return view
}

func mustFloat32View(t *testing.T, data []float32) dnnblob.Float32View {
	t.Helper()
	bytes := make([]byte, 4*len(data))
	for i, v := range data {
		binary.LittleEndian.PutUint32(bytes[i*4:i*4+4], math.Float32bits(v))
	}
	view, err := dnnblob.Float32ViewFromBytes(bytes, int32(len(bytes)))
	if err != nil {
		t.Fatalf("Float32ViewFromBytes: %v", err)
	}
	return view
}

func approxEqual(a, b, eps float32) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	// Compare in absolute terms; the int32 accumulator may grow to ~2^17
	// for rows of 384x128 with full-range int8 weights and inputs, so we
	// allow a small absolute tolerance scaled by the magnitude.
	mag := a
	if mag < 0 {
		mag = -mag
	}
	if b > mag {
		mag = b
	} else if -b > mag {
		mag = -b
	}
	return d <= eps*(1+mag)
}
