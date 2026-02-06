package testvectors

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func TestSilkNB10msChirpDebug(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bandwidth types.Bandwidth
		frameSize int
	}{
		{"NB-10ms", types.BandwidthNarrowband, 480},
		{"NB-20ms", types.BandwidthNarrowband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(encoder.ModeSILK)
			enc.SetBandwidth(tc.bandwidth)
			enc.SetBitrate(32000)

			dec, err := gopus.NewDecoder(gopus.DecoderConfig{SampleRate: 48000, Channels: 1})
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}

			numFrames := 48000 / tc.frameSize
			totalSamples := numFrames * tc.frameSize
			signal := make([]float32, totalSamples)
			for i := 0; i < totalSamples; i++ {
				ti := float64(i) / 48000.0
				freq := 200.0 + 1800.0*ti
				signal[i] = float32(0.5 * math.Sin(2*math.Pi*freq*ti))
			}

			decodeBuf := make([]float32, 5760)
			var totalDecodedRMS, totalInputRMS float64
			var totalDecSamples, totalInSamples int

			for i := 0; i < numFrames; i++ {
				start := i * tc.frameSize
				end := start + tc.frameSize
				pcm := float32ToFloat64(signal[start:end])

				// Input RMS
				var inE float64
				for _, v := range signal[start:end] {
					inE += float64(v) * float64(v)
				}
				inRMS := math.Sqrt(inE / float64(tc.frameSize))

				pkt, err := enc.Encode(pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("Encode frame %d: %v", i, err)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)

				n, err := dec.Decode(cp, decodeBuf)
				if err != nil {
					t.Fatalf("Decode frame %d: %v", i, err)
				}

				// Output RMS
				var outE float64
				for j := 0; j < n; j++ {
					outE += float64(decodeBuf[j]) * float64(decodeBuf[j])
				}
				outRMS := math.Sqrt(outE / float64(n))

				totalDecodedRMS += outE
				totalDecSamples += n
				totalInputRMS += inE
				totalInSamples += tc.frameSize

				ratio := 0.0
				if inRMS > 0 {
					ratio = outRMS / inRMS * 100
				}

				if i < 5 || i == numFrames-1 || (i >= 5 && i < 10) {
					t.Logf("Frame %2d: %d bytes, n=%d, inRMS=%.4f outRMS=%.4f ratio=%.1f%%",
						i, len(cp), n, inRMS, outRMS, ratio)
				}
			}

			overallIn := math.Sqrt(totalInputRMS / float64(totalInSamples))
			overallOut := math.Sqrt(totalDecodedRMS / float64(totalDecSamples))
			t.Logf("Overall: inRMS=%.4f outRMS=%.4f ratio=%.1f%%, frames=%d, totalDecSamples=%d, totalInSamples=%d",
				overallIn, overallOut, overallOut/overallIn*100, numFrames, totalDecSamples, totalInSamples)
		})
	}
}
