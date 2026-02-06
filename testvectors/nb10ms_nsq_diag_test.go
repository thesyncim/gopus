package testvectors

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestNB10msComplexity tests NB-10ms with different complexity levels
// to check if the delayed-decision quantizer causes the amplitude inflation.
func TestNB10msComplexity(t *testing.T) {
	for _, complexity := range []int{0, 2, 5, 10} {
		for _, tc := range []struct {
			name      string
			bandwidth types.Bandwidth
			frameSize int
		}{
			{"NB-10ms", types.BandwidthNarrowband, 480},
			{"NB-20ms", types.BandwidthNarrowband, 960},
		} {
			t.Run(tc.name+"-c"+itoa(complexity), func(t *testing.T) {
				channels := 1
				bitrate := 32000
				sampleRate := 48000

				// Same chirp signal as original test
				numFrames := sampleRate / tc.frameSize
				totalSamples := numFrames * tc.frameSize * channels
				signal := make([]float32, totalSamples)
				for i := 0; i < totalSamples; i++ {
					ti := float64(i) / float64(sampleRate)
					freq := 200.0 + 1800.0*ti
					signal[i] = float32(0.5 * math.Sin(2*math.Pi*freq*ti))
				}

				enc := encoder.NewEncoder(sampleRate, channels)
				enc.SetMode(encoder.ModeSILK)
				enc.SetBandwidth(tc.bandwidth)
				enc.SetBitrate(bitrate)
				enc.SetComplexity(complexity)

				dec, err := gopus.NewDecoder(gopus.DecoderConfig{
					SampleRate: sampleRate,
					Channels:   channels,
				})
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}

				decoded := make([]float32, 0, totalSamples+5760)
				decodeBuf := make([]float32, 5760)

				for i := 0; i < numFrames; i++ {
					start := i * tc.frameSize * channels
					end := start + tc.frameSize*channels
					pcm := float32ToFloat64(signal[start:end])
					pkt, err := enc.Encode(pcm, tc.frameSize)
					if err != nil {
						t.Fatalf("Encode frame %d: %v", i, err)
					}
					if len(pkt) == 0 {
						t.Fatalf("Empty packet at frame %d", i)
					}
					cp := make([]byte, len(pkt))
					copy(cp, pkt)

					n, err := dec.Decode(cp, decodeBuf)
					if err != nil {
						t.Fatalf("Decode frame %d: %v", i, err)
					}
					decoded = append(decoded, decodeBuf[:n*channels]...)
				}

				var decRMS, refRMS float64
				for _, s := range decoded {
					decRMS += float64(s) * float64(s)
				}
				decRMS = math.Sqrt(decRMS / float64(len(decoded)))
				for _, s := range signal {
					refRMS += float64(s) * float64(s)
				}
				refRMS = math.Sqrt(refRMS / float64(len(signal)))

				var maxDec float32
				for _, s := range decoded {
					if s > maxDec {
						maxDec = s
					}
					if -s > maxDec {
						maxDec = -s
					}
				}

				t.Logf("complexity=%d: decRMS=%.4f refRMS=%.4f ratio=%.1f%% maxDec=%.4f",
					complexity, decRMS, refRMS, decRMS/refRMS*100, maxDec)
			})
		}
	}
}
