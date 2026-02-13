package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestSILKMaxBitsReservesTOCByte(t *testing.T) {
	const (
		frameSize = 960
		bitrate   = 24000
	)

	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthMediumband)
	enc.SetBitrateMode(ModeCBR)
	enc.SetBitrate(bitrate)

	trace := &silk.EncoderTrace{GainLoop: &silk.GainLoopTrace{}}
	enc.SetSilkTrace(trace)

	pcm := make([]float64, frameSize)
	for i := range pcm {
		pcm[i] = 0.25 * math.Sin(2*math.Pi*440*float64(i)/48000.0)
	}
	for i := 0; i < 2; i++ {
		trace.GainLoop = &silk.GainLoopTrace{}
		if _, err := enc.Encode(pcm, frameSize); err != nil {
			t.Fatalf("encode frame %d: %v", i, err)
		}
	}

	got := trace.GainLoop.MaxBits
	want := silkPayloadMaxBits(targetBytesForBitrate(bitrate, frameSize))
	if got != want {
		t.Fatalf("unexpected SILK maxBits: got=%d want=%d", got, want)
	}
}
