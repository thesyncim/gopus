package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

func TestPostfilterFlagEnabledForTone(t *testing.T) {
	const (
		frameSize  = 960
		sampleRate = 48000
		bitrate    = 64000
		freqHz     = 440.0
		amp        = 0.5
	)

	pcm := make([]float64, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = amp * math.Sin(2*math.Pi*freqHz*ti)
	}

	enc := NewEncoder(1)
	enc.SetBitrate(bitrate)

	packet, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	rd := &rangecoding.Decoder{}
	rd.Init(packet)

	silence := rd.DecodeBit(15)
	if silence != 0 {
		t.Fatalf("unexpected silence flag=%d for tone", silence)
	}

	postfilter := rd.DecodeBit(1)
	if postfilter == 0 {
		t.Fatalf("postfilter flag=0 for tone at %d bps", bitrate)
	}
}
