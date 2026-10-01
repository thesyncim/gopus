//go:build gopus_fixed_point

package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// TestFixedOuterCBRInputPreprocessMatchesLibopus verifies the exact Q8 input
// sequence presented to fixed SILK for the 10 ms CBR regression signal. It
// includes the quiet onset frames so filter history starts before the first
// frame that carries substantial signal energy.
func TestFixedOuterCBRInputPreprocessMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 480
		frames    = 3
	)
	pcm, err := testsignal.GenerateEncoderSignalVariant(testsignal.EncoderVariantAMMultisineV1, 48000, frameSize*frames, 1)
	if err != nil {
		t.Fatal(err)
	}

	quantized := make([]float32, len(pcm))
	raw := make([]int32, len(pcm))
	for i, sample := range pcm {
		// Match the public CBR oracle's opus_demo -f32 quantization before
		// both paths enter FLOAT2RES.
		q := float32(math.Floor(0.5+float64(sample)*8388608.0) / 8388608.0)
		quantized[i] = q
		raw[i] = fixedFloatToRes(q)
	}
	want, wantMem, err := libopustest.ProbeFixedDCReject(raw, [4]int32{}, 48000, 1, 3)
	if err != nil {
		t.Fatal(err)
	}

	e := NewEncoder(48000, 1)
	got := make([]int32, 0, len(raw))
	for frame := range frames {
		start := frame * frameSize
		e.prepareFixedInputRes(quantized[start : start+frameSize])
		e.preprocessFixedInputRes(frameSize)
		got = append(got, e.fixedFiltered...)
	}
	if len(got) != len(want) {
		t.Fatalf("filtered samples=%d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("filtered input sample %d (frame %d): Go=%d C=%d raw=%d", i, i/frameSize, got[i], want[i], raw[i])
		}
	}
	if e.fixedHPMem != wantMem {
		t.Fatalf("filter state after onset: Go=%v C=%v", e.fixedHPMem, wantMem)
	}
}
