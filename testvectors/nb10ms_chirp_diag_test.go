package testvectors

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestNB10msChirpDiag reproduces the exact conditions of the original roundtrip test
// with per-frame analysis to find where the inflation happens.
func TestNB10msChirpDiag(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bandwidth types.Bandwidth
		frameSize int
	}{
		{"NB-10ms", types.BandwidthNarrowband, 480},
		{"NB-20ms", types.BandwidthNarrowband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channels := 1
			bitrate := 32000
			sampleRate := 48000

			// Exact same chirp signal as in TestSilk10msRoundtrip
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

				frameDecoded := decodeBuf[:n*channels]

				// Per-frame RMS
				if i >= 5 && i < 15 {
					var decE, refE float64
					for _, s := range frameDecoded {
						decE += float64(s) * float64(s)
					}
					for j := start; j < end; j++ {
						refE += float64(signal[j]) * float64(signal[j])
					}
					decRMS := math.Sqrt(decE / float64(len(frameDecoded)))
					refRMS := math.Sqrt(refE / float64(end-start))

					// Also check max amplitude
					var maxDec float32
					for _, s := range frameDecoded {
						if s > maxDec {
							maxDec = s
						}
						if -s > maxDec {
							maxDec = -s
						}
					}

					t.Logf("Frame %d: pkt=%d, dec=%d samp, decRMS=%.4f refRMS=%.4f ratio=%.1f%% maxDec=%.4f",
						i, len(cp), n, decRMS, refRMS, decRMS/refRMS*100, maxDec)
				}

				// Log frames near the end too (where chirp freq is highest)
				if i >= numFrames-5 {
					var decE, refE float64
					for _, s := range frameDecoded {
						decE += float64(s) * float64(s)
					}
					for j := start; j < end; j++ {
						refE += float64(signal[j]) * float64(signal[j])
					}
					decRMS := math.Sqrt(decE / float64(len(frameDecoded)))
					refRMS := math.Sqrt(refE / float64(end-start))
					var maxDec float32
					for _, s := range frameDecoded {
						if s > maxDec {
							maxDec = s
						}
						if -s > maxDec {
							maxDec = -s
						}
					}
					t.Logf("Frame %d: pkt=%d, dec=%d samp, decRMS=%.4f refRMS=%.4f ratio=%.1f%% maxDec=%.4f",
						i, len(cp), n, decRMS, refRMS, decRMS/refRMS*100, maxDec)
				}

				decoded = append(decoded, frameDecoded...)
			}

			// Total RMS like the original test
			var decRMS, refRMS float64
			for _, s := range decoded {
				decRMS += float64(s) * float64(s)
			}
			decRMS = math.Sqrt(decRMS / float64(len(decoded)))
			for _, s := range signal {
				refRMS += float64(s) * float64(s)
			}
			refRMS = math.Sqrt(refRMS / float64(len(signal)))
			t.Logf("TOTAL: decoded=%d, signal=%d, decRMS=%.6f, refRMS=%.6f, ratio=%.1f%%",
				len(decoded), len(signal), decRMS, refRMS, decRMS/refRMS*100)

			// Also check: how many decoded samples have |amplitude| > 0.5 (input max)?
			countOverInput := 0
			for _, s := range decoded {
				if s > 0.5 || s < -0.5 {
					countOverInput++
				}
			}
			t.Logf("Samples with |amplitude| > 0.5: %d / %d (%.1f%%)",
				countOverInput, len(decoded), float64(countOverInput)/float64(len(decoded))*100)
		})
	}
}
