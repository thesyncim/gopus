//go:build gopus_osce

package bwe

import (
	"math"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dnnmath"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func loadSelectedBWEBlob(t *testing.T) *dnnblob.Blob {
	t.Helper()
	libopustest.RequireOracle(t)
	wd, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(wd, "../../.."))
	helper, err := bweModelBlobOracle.Path(func() (string, error) {
		return libopustest.BuildOSCEHelper(root, "libopus_osce_bwe_model_blob.c", "gopus_bwe_selected_kernel_model", true)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE BWE model blob", err)
	}
	data, err := libopustest.RunHelper(helper, nil)
	if err != nil {
		t.Fatalf("OSCE BWE model blob: %v", err)
	}
	blob, err := dnnblob.Clone(data)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	return blob
}

func TestFNetConv1UsesSelectedDNNLinearKernel(t *testing.T) {
	blob := loadSelectedBWEBlob(t)
	var state State
	if err := state.SetModel(blob); err != nil {
		t.Fatal(err)
	}

	pcm := makeBWESequenceInput(1)
	var featureState FeatureState
	featureState.Reset()
	var features [FeatureDim]float32
	featureState.CalculateFeatures(features[:], pcm[:160])
	var input [FNetConv1In]float32
	copy(input[:FNetConv1In-FeatureDim], state.fnetConv1State[:])
	copy(input[FNetConv1In-FeatureDim:], features[:])

	const rows, cols = FNetConv1Out, FNetConv1In
	weights := make([]float32, rows*cols)
	for i := range weights {
		weights[i] = state.model.FNetConv1.FloatWeights.At(i)
	}
	payload := libopustest.NewOraclePayload("GDKI", libopustest.DNNKernelSGEMV, uint32(rows), uint32(cols), uint32(rows))
	payload.Float32s(weights...)
	payload.Float32s(input[:]...)
	helper, err := libopustest.DNNKernelOraclePath()
	if err != nil {
		libopustest.HelperUnavailable(t, "selected DNN linear kernel", err)
	}
	reader, err := libopustest.RunOracle(helper, payload.Bytes(), "OSCE FNetConv1 selected linear kernel", "GDKO")
	if err != nil {
		libopustest.HelperUnavailable(t, "selected DNN linear kernel", err)
	}
	if got := reader.Count(rows); got != rows {
		t.Fatalf("linear output count=%d, want %d", got, rows)
	}
	arch := reader.U32()
	if err := libopustest.ValidateDNNDispatchArch(arch); err != nil {
		t.Fatal(err)
	}
	if runtime.GOARCH == "amd64" && (arch == 4) != dnnmath.X86VectorKernels {
		t.Fatalf("selected C DNN arch=%d, Go AVX2 dispatch=%t", arch, dnnmath.X86VectorKernels)
	}
	t.Logf("selected C DNN arch=%d, Go AVX2 dispatch=%t", arch, dnnmath.X86VectorKernels)
	reader.ExpectRemaining(rows * 4)
	want := make([]float32, rows)
	for i := range want {
		want[i] = reader.Float32() + state.model.FNetConv1.Bias.At(i)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	got := make([]float32, rows)
	computeLinear(&state.model.FNetConv1, got, input[:])
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("FNetConv1 output[%d]=%08x, selected C=%08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func TestBWEProcessDoesNotAllocateAfterWarmup(t *testing.T) {
	blob := loadSelectedBWEBlob(t)
	var state State
	if err := state.SetModel(blob); err != nil {
		t.Fatal(err)
	}

	pcm := makeBWESequenceInput(1)
	var featureState FeatureState
	featureState.Reset()
	var features [FeatureDim]float32
	featureState.CalculateFeatures(features[:], pcm[:160])
	var input [160]float32
	for i, sample := range pcm[:160] {
		input[i] = float32(sample) * (1.0 / 32768.0)
	}
	var output [480]float32
	if err := state.Process(input[:], output[:], features[:]); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		if err := state.Process(input[:], output[:], features[:]); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("warm BWE frame allocs=%g, want 0", allocs)
	}
}
