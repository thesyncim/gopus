//go:build gopus_dred || gopus_osce

package gopus

import (
	"math"
	"testing"
)

func TestDecodeDREDInt24NoOverflowOrPrematureAlloc(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	initialCap := cap(dec.scratchPCM)

	// Overflowing frameSizeSamples * channels must return an error, never panic.
	pcm24 := make([]int32, 960*2)
	if _, err := dec.DecodeDREDInt24(NewDRED(), 0, pcm24, math.MaxInt/2+1); err == nil {
		t.Fatal("DecodeDREDInt24 accepted overflowing frameSizeSamples")
	}
	if cap(dec.scratchPCM) != initialCap {
		t.Fatalf("DecodeDREDInt24 grew scratchPCM cap from %d to %d on invalid call", initialCap, cap(dec.scratchPCM))
	}

	pcmF32 := make([]float32, 960*2)
	if _, err := dec.DecodeDRED(NewDRED(), 0, pcmF32, math.MaxInt/2+1); err == nil {
		t.Fatal("DecodeDRED accepted overflowing frameSizeSamples")
	}
}
