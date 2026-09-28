package bwe

import (
	"math"
	"math/rand"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestCGEMV8x4MatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0xBBEED10))
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
		t.Run(testShapeName(tc.rows, tc.cols), func(t *testing.T) {
			weights, scale := makeRandomInt8LayerData(rng, tc.rows, tc.cols)
			in := make([]float32, tc.cols)
			for i := range in {
				in[i] = float32(rng.Float64()*2 - 1)
			}
			want, err := libopustest.ProbeDNNKernelCGEMV8x4(
				tc.rows, tc.cols, int8WeightsForOracle(weights), scale, in,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "selected DNN cgemv8x4", err)
			}

			got := make([]float32, tc.rows)
			cgemv8x4(got, mustInt8View(t, weights), mustFloat32View(t, scale), tc.rows, tc.cols, in)
			assertDNNFloatBits(t, got, want)
		})
	}
}

func TestComputeLinearInt8MatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0xC0FFEE))
	const rows, cols = 256, 128
	weights, scale := makeRandomInt8LayerData(rng, rows, cols)
	bias := make([]float32, rows)
	subias := make([]float32, rows)
	for i := range bias {
		bias[i] = float32(rng.Float64()*0.2 - 0.1)
		subias[i] = float32(rng.Float64()*0.2 - 0.1)
	}
	in := make([]float32, cols)
	for i := range in {
		in[i] = float32(rng.Float64()*2 - 1)
	}

	want, err := libopustest.ProbeDNNLinearCGEMV8x4(
		rows, cols, nil, int8WeightsForOracle(weights), scale, in, bias, subias,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected DNN int8 compute_linear", err)
	}
	layer := &LinearLayer{
		Bias:      mustFloat32View(t, bias),
		Subias:    mustFloat32View(t, subias),
		Weights:   mustInt8View(t, weights),
		Scale:     mustFloat32View(t, scale),
		NbInputs:  cols,
		NbOutputs: rows,
	}
	got := make([]float32, rows)
	computeLinear(layer, got, in)
	assertDNNFloatBits(t, got, want)
}

func int8WeightsForOracle(weights []int8) []byte {
	encoded := make([]byte, len(weights))
	for i, weight := range weights {
		encoded[i] = byte(weight)
	}
	return encoded
}

func assertDNNFloatBits(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("output lengths Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("output[%d] Go=%08x selected C=%08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func testShapeName(rows, cols int) string {
	return "rows_" + strconv.Itoa(rows) + "_cols_" + strconv.Itoa(cols)
}
