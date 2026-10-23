//go:build gopus_libopus_bench

package gopus_test

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// BenchmarkCELTShortFrameDecode profiles the public decoder over the scoreboard's
// CELT 2.5 ms music corpus and controls without running the libopus timing helper.
// Each operation resets the stream and decodes one second of pre-encoded packets;
// ns/op and ns/packet include that reset. Use BenchmarkScoreboardDecode or
// TestScoreboardInterleaved for matched libopus comparisons.
func BenchmarkCELTShortFrameDecode(b *testing.B) {
	for _, c := range scoreboardConfigs() {
		if c.Mode != gopus.EncoderModeCELT || c.Frame48 != 120 {
			continue
		}
		b.Run(c.name(), func(b *testing.B) {
			nFrames := c.frameCount()
			frameSize := c.nativeFrame()
			pcm, err := genGopusPCM(c, nFrames)
			if err != nil {
				b.Fatal(err)
			}
			packets, _ := encodeGopusBatch(b, c, pcm, nFrames)
			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(c.Rate, c.Channels))
			if err != nil {
				b.Fatal(err)
			}
			pcmOut := make([]float32, frameSize*c.Channels)
			decodeStream := func() {
				dec.Reset()
				for i, packet := range packets {
					n, err := dec.Decode(packet, pcmOut)
					if err != nil || n != frameSize {
						b.Fatalf("Decode packet %d: samples=%d want=%d, error=%v", i, n, frameSize, err)
					}
				}
			}
			decodeStream()
			if allocs := testing.AllocsPerRun(2, decodeStream); allocs != 0 {
				b.Fatalf("warm allocations per stream=%g, want 0", allocs)
			}
			b.ReportAllocs()
			for b.Loop() {
				decodeStream()
			}
			b.ReportMetric(float64(len(packets)), "packets/op")
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(packets)), "ns/packet")
		})
	}
}
