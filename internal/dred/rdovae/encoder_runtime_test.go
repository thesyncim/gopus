package rdovae

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func TestEncodeDFrameZeroModelOutputsZeroLatentsAndState(t *testing.T) {
	blob, err := dnnblob.Clone(buildEncoderTestBlob())
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	model, err := LoadEncoder(blob)
	if err != nil {
		t.Fatalf("LoadEncoder error: %v", err)
	}

	input := make([]float32, encoderInputSize)
	for i := range input {
		input[i] = float32(i + 1)
	}
	latents := make([]float32, LatentDim)
	state := make([]float32, StateDim)
	if !model.EncodeDFrame(latents, state, input) {
		t.Fatal("EncodeDFrame returned false")
	}
	for i, v := range latents {
		if v != 0 {
			t.Fatalf("latents[%d]=%v want 0 for zeroed test model", i, v)
		}
	}
	for i, v := range state {
		if v != 0 {
			t.Fatalf("state[%d]=%v want 0 for zeroed test model", i, v)
		}
	}
}

func TestEncodeDFrameDoesNotAllocate(t *testing.T) {
	blob, err := dnnblob.Clone(buildEncoderTestBlob())
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	model, err := LoadEncoder(blob)
	if err != nil {
		t.Fatalf("LoadEncoder error: %v", err)
	}

	input := make([]float32, encoderInputSize)
	latents := make([]float32, LatentDim)
	state := make([]float32, StateDim)
	var processor EncoderProcessor

	allocs := testing.AllocsPerRun(1000, func() {
		if !model.EncodeDFrameWithProcessor(&processor, latents, state, input) {
			t.Fatal("EncodeDFrameWithProcessor returned false")
		}
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}
