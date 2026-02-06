package testvectors

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestNB10msLowFreqSine tests NB-10ms with a low frequency sine that matches
// the chirp's frequency range where inflation occurs (frames 5-14).
func TestNB10msLowFreqSine(t *testing.T) {
	for _, freq := range []float64{200, 300, 440, 1000, 2000} {
		for _, tc := range []struct {
			name      string
			bandwidth types.Bandwidth
			frameSize int
		}{
			{"NB-10ms", types.BandwidthNarrowband, 480},
			{"NB-20ms", types.BandwidthNarrowband, 960},
		} {
			t.Run(tc.name+"-"+itoa(int(freq))+"Hz", func(t *testing.T) {
				channels := 1
				bitrate := 32000
				sampleRate := 48000

				numFrames := 50
				totalSamples := numFrames * tc.frameSize * channels
				signal := make([]float32, totalSamples)
				for i := range signal {
					ti := float64(i) / float64(sampleRate)
					signal[i] = float32(0.5 * math.Sin(2*math.Pi*freq*ti))
				}

				enc := encoder.NewEncoder(sampleRate, channels)
				enc.SetMode(encoder.ModeSILK)
				enc.SetBandwidth(tc.bandwidth)
				enc.SetBitrate(bitrate)

				dec, err := gopus.NewDecoder(gopus.DecoderConfig{
					SampleRate: sampleRate,
					Channels:   channels,
				})
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}

				decodeBuf := make([]float32, 5760)
				var totalDecEnergy, totalRefEnergy float64
				var totalDecCount, totalRefCount int
				var maxDec float32

				for i := 0; i < numFrames; i++ {
					start := i * tc.frameSize * channels
					end := start + tc.frameSize*channels
					pcm := float32ToFloat64(signal[start:end])
					pkt, err := enc.Encode(pcm, tc.frameSize)
					if err != nil {
						t.Fatalf("Encode frame %d: %v", i, err)
					}
					if len(pkt) == 0 {
						continue
					}
					cp := make([]byte, len(pkt))
					copy(cp, pkt)

					n, err := dec.Decode(cp, decodeBuf)
					if err != nil {
						t.Fatalf("Decode frame %d: %v", i, err)
					}

					if i >= 5 { // Skip warmup
						decoded := decodeBuf[:n*channels]
						for _, s := range decoded {
							totalDecEnergy += float64(s) * float64(s)
							totalDecCount++
							if s > maxDec {
								maxDec = s
							}
							if -s > maxDec {
								maxDec = -s
							}
						}
						for j := start; j < end; j++ {
							totalRefEnergy += float64(signal[j]) * float64(signal[j])
							totalRefCount++
						}
					}
				}

				decRMS := math.Sqrt(totalDecEnergy / float64(totalDecCount))
				refRMS := math.Sqrt(totalRefEnergy / float64(totalRefCount))
				t.Logf("%.0fHz: decRMS=%.4f refRMS=%.4f ratio=%.1f%% maxDec=%.4f",
					freq, decRMS, refRMS, decRMS/refRMS*100, maxDec)
			})
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
