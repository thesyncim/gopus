package rdovae

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dnnmath"
)

func TestDecodeAllZeroModelOutputsZeroFeatures(t *testing.T) {
	blob, err := dnnblob.Clone(buildDecoderTestBlob())
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	model, err := LoadDecoder(blob)
	if err != nil {
		t.Fatalf("LoadDecoder error: %v", err)
	}

	state := make([]float32, StateDim)
	latents := make([]float32, latentStride)
	for i := range state {
		state[i] = float32(i + 1)
	}
	for i := range latents {
		latents[i] = float32(i + 1)
	}
	features := make([]float32, OutputOutSize)

	if n := model.DecodeAll(features, state, latents, 1); n != OutputOutSize {
		t.Fatalf("DecodeAll count=%d want %d", n, OutputOutSize)
	}
	for i, v := range features {
		if v != 0 {
			t.Fatalf("features[%d]=%v want 0 for zeroed test model", i, v)
		}
	}
}

func TestDecodeAllDoesNotAllocate(t *testing.T) {
	blob, err := dnnblob.Clone(buildDecoderTestBlob())
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	model, err := LoadDecoder(blob)
	if err != nil {
		t.Fatalf("LoadDecoder error: %v", err)
	}

	state := make([]float32, StateDim)
	latents := make([]float32, latentStride)
	features := make([]float32, OutputOutSize)
	var processor Processor

	allocs := testing.AllocsPerRun(1000, func() {
		if n := model.DecodeAllWithProcessor(&processor, features, state, latents, 1); n != OutputOutSize {
			t.Fatalf("DecodeAll count=%d want %d", n, OutputOutSize)
		}
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}

func TestComputeActivationUsesLibopusVectorTail(t *testing.T) {
	input := []float32{-0.75}

	gotTanh := []float32{0}
	computeActivation(gotTanh, input, len(input), activationTanh)
	wantTanh := []float32{0}
	dnnmath.TanhVectorApprox(wantTanh, input, len(input))
	if got, want := math.Float32bits(gotTanh[0]), math.Float32bits(wantTanh[0]); got != want {
		t.Fatalf("tanh tail bits=0x%08x want 0x%08x", got, want)
	}

	gotSigmoid := []float32{0}
	computeActivation(gotSigmoid, input, len(input), activationSigmoid)
	wantSigmoid := []float32{0}
	dnnmath.SigmoidVectorApprox(wantSigmoid, input, len(input))
	if got, want := math.Float32bits(gotSigmoid[0]), math.Float32bits(wantSigmoid[0]); got != want {
		t.Fatalf("sigmoid tail bits=0x%08x want 0x%08x", got, want)
	}
}
