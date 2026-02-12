package testvectors

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestSilk10msRoundtrip tests SILK encode→decode roundtrip quality at 10ms and 20ms
// using the gopus decoder directly (no Ogg container, no opusdec).
// This isolates encoder quality from container/tool issues.
func TestSilk10msRoundtrip(t *testing.T) {
	requireTestTier(t, testTierParity)

	for _, tc := range []struct {
		name      string
		bandwidth types.Bandwidth
		frameSize int
	}{
		{"NB-10ms", types.BandwidthNarrowband, 480},
		{"NB-20ms", types.BandwidthNarrowband, 960},
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channels := 1
			bitrate := 32000
			sampleRate := 48000

			// Use a chirp signal for better delay detection (not pure sine)
			numFrames := sampleRate / tc.frameSize
			totalSamples := numFrames * tc.frameSize * channels
			signal := make([]float32, totalSamples)
			for i := 0; i < totalSamples; i++ {
				ti := float64(i) / float64(sampleRate)
				// Chirp: frequency sweeps from 200 Hz to 2000 Hz over 1 second
				freq := 200.0 + 1800.0*ti
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

			decoded := make([]float32, 0, totalSamples+5760)
			decodeBuf := make([]float32, 5760) // max opus frame size

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
				// Copy packet to avoid scratch buffer reuse
				cp := make([]byte, len(pkt))
				copy(cp, pkt)

				n, err := dec.Decode(cp, decodeBuf)
				if err != nil {
					t.Fatalf("Decode frame %d: %v", i, err)
				}
				decoded = append(decoded, decodeBuf[:n*channels]...)
			}

			// Compute SNR with delay search
			bestSNR := math.Inf(-1)
			bestDelay := 0
			margin := 480 // skip edges to avoid startup transients
			for d := -4000; d <= 4000; d++ {
				var signalPower, noisePower float64
				count := 0
				for i := margin; i < len(signal)-margin; i++ {
					decIdx := i + d
					if decIdx >= margin && decIdx < len(decoded)-margin {
						ref := float64(signal[i])
						out := float64(decoded[decIdx])
						signalPower += ref * ref
						noise := out - ref
						noisePower += noise * noise
						count++
					}
				}
				if count > 0 && signalPower > 0 && noisePower > 0 {
					snr := 10.0 * math.Log10(signalPower/noisePower)
					if snr > bestSNR {
						bestSNR = snr
						bestDelay = d
					}
				}
			}
			t.Logf("SNR=%.2f dB, delay=%d, decoded=%d samples, original=%d samples", bestSNR, bestDelay, len(decoded), len(signal))

			// Check RMS
			var decRMS, refRMS float64
			for _, s := range decoded {
				decRMS += float64(s) * float64(s)
			}
			decRMS = math.Sqrt(decRMS / float64(len(decoded)))
			for _, s := range signal {
				refRMS += float64(s) * float64(s)
			}
			refRMS = math.Sqrt(refRMS / float64(len(signal)))
			t.Logf("Decoded RMS: %.6f, Reference RMS: %.6f, ratio: %.1f%%", decRMS, refRMS, decRMS/refRMS*100)

			// The test itself doesn't fail on quality -- it's diagnostic.
			// But report status:
			if bestSNR >= 24.0 {
				t.Logf("STATUS: GOOD (>= 24 dB)")
			} else if bestSNR >= 10.0 {
				t.Logf("STATUS: ACCEPTABLE (>= 10 dB)")
			} else {
				t.Logf("STATUS: LOW (< 10 dB) -- needs investigation")
			}
		})
	}
}
