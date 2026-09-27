package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestBandDecodeWritesEveryCoefficient decodes random CELT payloads twice, once
// with the band output scratch poisoned with NaN before every frame, and checks
// the outputs agree bit for bit: quant_all_bands writes every coefficient it
// leaves in place, so the scratch needs no clearing inside the coded bands.
func TestBandDecodeWritesEveryCoefficient(t *testing.T) {
	rng := rand.New(rand.NewSource(0xc0ef))
	nan := float32(math.NaN())
	poison := func(buf []celtNorm) {
		buf = buf[:cap(buf)]
		for i := range buf {
			buf[i] = nan
		}
	}
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{120, 240, 480, 960} {
			ref := NewDecoder(channels)
			got := NewDecoder(channels)
			for frame := 0; frame < 60; frame++ {
				payload := make([]byte, 2+rng.Intn(160))
				rng.Read(payload)
				want, err := ref.DecodeFrame(payload, frameSize)
				if err != nil {
					t.Fatal(err)
				}
				want = append([]float32(nil), want...)
				poison(got.scratchBands.left)
				poison(got.scratchBands.right)
				out, err := got.DecodeFrame(payload, frameSize)
				if err != nil {
					t.Fatal(err)
				}
				for _, band := range [][]celtNorm{got.scratchBands.left, got.scratchBands.right} {
					for i, v := range band {
						if v != v {
							t.Fatalf("C=%d N=%d frame %d: coefficient %d left unwritten", channels, frameSize, frame, i)
						}
					}
				}
				for i := range want {
					if math.Float32bits(out[i]) != math.Float32bits(want[i]) {
						t.Fatalf("C=%d N=%d frame %d: sample %d = %v, want %v", channels, frameSize, frame, i, out[i], want[i])
					}
				}
			}
		}
	}
}
