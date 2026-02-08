package gopus_test

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

func BenchmarkStereoProfile(b *testing.B) {
	enc, err := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
	if err != nil {
		b.Fatal(err)
	}
	_ = enc.SetBitrate(128000)
	_ = enc.SetComplexity(10)

	pcm := make([]float32, 960*2)
	for i := range pcm {
		t := float64(i) / float64(48000*2)
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*440*t))
	}
	pkt := make([]byte, 4000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc.Encode(pcm, pkt)
	}
}
