package rdovae

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
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
