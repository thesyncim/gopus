package testvectors

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestSilkNB10msDebug is a focused debug test for the NB-10ms amplitude blow-up.
// NB-10ms decoded RMS is 173% of input -- something is amplifying the signal.
func TestSilkNB10msDebug(t *testing.T) {
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

			// Use simple 440 Hz sine wave for consistent energy
			numFrames := 50
			decodeBuf := make([]float32, 5760)

			for i := 0; i < numFrames; i++ {
				pcmF64 := make([]float64, tc.frameSize)
				pcmF32 := make([]float32, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					v := 0.5 * math.Sin(2*math.Pi*440.0*float64(sampleIdx)/48000.0)
					pcmF64[j] = v
					pcmF32[j] = float32(v)
				}

				// Compute input RMS
				var inEnergy float64
				for _, v := range pcmF32 {
					inEnergy += float64(v) * float64(v)
				}
				inRMS := math.Sqrt(inEnergy / float64(len(pcmF32)))

				// Encode
				pkt, err := enc.Encode(pcmF64, tc.frameSize)
				if err != nil {
					t.Fatalf("Encode frame %d: %v", i, err)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)

				// Decode
				n, err := dec.Decode(cp, decodeBuf)
				if err != nil {
					t.Fatalf("Decode frame %d: %v", i, err)
				}

				// Compute output RMS
				var outEnergy float64
				for j := 0; j < n; j++ {
					outEnergy += float64(decodeBuf[j]) * float64(decodeBuf[j])
				}
				outRMS := math.Sqrt(outEnergy / float64(n))

				ratio := outRMS / inRMS * 100
				if i >= 3 && i < 12 { // Skip first few warmup, show some detail
					t.Logf("Frame %d: %d bytes, TOC=0x%02x, decoded=%d, inRMS=%.4f outRMS=%.4f ratio=%.1f%%",
						i, len(cp), cp[0], n, inRMS, outRMS, ratio)
				}
			}
		})
	}
}
