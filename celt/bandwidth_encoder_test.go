package celt

import "testing"

func TestEffectiveBandCountRespectsBandwidth(t *testing.T) {
	enc := NewEncoder(1)
	for _, tc := range []struct {
		name      string
		bw        CELTBandwidth
		frameSize int
	}{
		{name: "nb-2.5ms", bw: CELTNarrowband, frameSize: 120},
		{name: "wb-10ms", bw: CELTWideband, frameSize: 480},
		{name: "swb-20ms", bw: CELTSuperwideband, frameSize: 960},
		{name: "fb-20ms", bw: CELTFullband, frameSize: 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc.SetBandwidth(tc.bw)
			got := enc.effectiveBandCount(tc.frameSize)
			want := EffectiveBandsForFrameSize(tc.bw, tc.frameSize)
			if got != want {
				t.Fatalf("effectiveBandCount=%d want=%d (bw=%v frameSize=%d)", got, want, tc.bw, tc.frameSize)
			}
		})
	}
}

func TestEncodeFrameBandwidthCapLimitsCodedBands(t *testing.T) {
	const frameSize = 960
	const bitrate = 256000

	pcm := make([]float64, frameSize)
	var s uint32 = 1
	for i := range pcm {
		s = s*1664525 + 1013904223
		pcm[i] = (float64(int32(s)) / 2147483648.0) * 0.8
	}

	full := NewEncoder(1)
	full.SetVBR(false)
	full.SetBitrate(bitrate)
	full.SetBandwidth(CELTFullband)
	if _, err := full.EncodeFrame(pcm, frameSize); err != nil {
		t.Fatalf("fullband encode failed: %v", err)
	}
	if full.lastCodedBands <= 13 {
		t.Fatalf("fullband coded bands unexpectedly low: %d", full.lastCodedBands)
	}

	nb := NewEncoder(1)
	nb.SetVBR(false)
	nb.SetBitrate(bitrate)
	nb.SetBandwidth(CELTNarrowband)
	if _, err := nb.EncodeFrame(pcm, frameSize); err != nil {
		t.Fatalf("narrowband encode failed: %v", err)
	}
	nbLimit := EffectiveBandsForFrameSize(CELTNarrowband, frameSize)
	if nb.lastCodedBands > nbLimit {
		t.Fatalf("narrowband coded bands exceeded cap: got=%d limit=%d", nb.lastCodedBands, nbLimit)
	}
}
