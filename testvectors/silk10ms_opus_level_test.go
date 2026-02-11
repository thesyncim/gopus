package testvectors

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestSILK10msOpusLevel tests SILK 10ms encoding at the Opus level using
// the gopus decoder to isolate whether the issue is in encoding or in opusdec.
func TestSILK10msOpusLevel(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"NB-10ms", types.BandwidthNarrowband, 480},
		{"NB-20ms", types.BandwidthNarrowband, 960},
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(encoder.ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			dec, err := gopus.NewDecoder(gopus.DecoderConfig{SampleRate: 48000, Channels: 1})
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}

			numFrames := 20
			totalSamples := numFrames * tc.frameSize
			pcmF64 := make([]float64, totalSamples)
			pcmF32 := make([]float32, totalSamples)
			for i := 0; i < totalSamples; i++ {
				v := 0.5 * math.Sin(2*math.Pi*440.0*float64(i)/48000.0)
				pcmF64[i] = v
				pcmF32[i] = float32(v)
			}

			decoded := make([]float32, 960)
			origRMS := 0.5 / math.Sqrt(2)
			var rmsSum float64
			var count int

			for i := 0; i < numFrames; i++ {
				start := i * tc.frameSize
				end := start + tc.frameSize

				packet, err := enc.Encode(pcmF64[start:end], tc.frameSize)
				if err != nil {
					t.Fatalf("Encode frame %d: %v", i, err)
				}
				if len(packet) == 0 {
					t.Logf("Frame %d: empty packet", i)
					continue
				}

				cp := make([]byte, len(packet))
				copy(cp, packet)

				// Check TOC byte
				toc := cp[0]
				config := toc >> 3

				n, err := dec.Decode(cp, decoded)
				if err != nil {
					t.Logf("Frame %d: decode error: %v", i, err)
					continue
				}

				var energy float64
				for j := 0; j < n; j++ {
					energy += float64(decoded[j]) * float64(decoded[j])
				}
				rms := math.Sqrt(energy / float64(n))

				if i >= 5 {
					ratio := rms / origRMS * 100
					rmsSum += rms
					count++
					t.Logf("Frame %d: %d bytes, config=%d, decoded=%d, RMS=%.4f ratio=%.1f%%",
						i, len(cp), config, n, rms, ratio)
				}
			}

			if count > 0 {
				avgRMS := rmsSum / float64(count)
				avgRatio := avgRMS / origRMS * 100
				t.Logf("Average RMS ratio: %.1f%%", avgRatio)
				if avgRatio < 50 {
					t.Errorf("Average RMS ratio %.1f%% is too low", avgRatio)
				}
			}
		})
	}
}
