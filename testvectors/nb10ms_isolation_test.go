package testvectors

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestNB10msIsolation compares NB-10ms and NB-20ms at the Opus level
// with per-frame RMS analysis to find exactly when/where the inflation starts.
func TestNB10msIsolation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bandwidth types.Bandwidth
		frameSize int // At 48kHz
	}{
		{"NB-10ms", types.BandwidthNarrowband, 480},
		{"NB-20ms", types.BandwidthNarrowband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channels := 1
			bitrate := 32000
			sampleRate := 48000

			// Use a simple sine wave for clarity
			numFrames := 30
			totalSamples := numFrames * tc.frameSize * channels
			signal := make([]float32, totalSamples)
			for i := 0; i < totalSamples; i++ {
				ti := float64(i) / float64(sampleRate)
				signal[i] = float32(0.5 * math.Sin(2*math.Pi*440.0*ti))
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

			for i := 0; i < numFrames; i++ {
				start := i * tc.frameSize * channels
				end := start + tc.frameSize*channels
				pcm := float32ToFloat64(signal[start:end])
				pkt, err := enc.Encode(pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("Encode frame %d: %v", i, err)
				}
				if len(pkt) == 0 {
					t.Logf("Frame %d: empty packet", i)
					continue
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)

				n, err := dec.Decode(cp, decodeBuf)
				if err != nil {
					t.Fatalf("Decode frame %d: %v", i, err)
				}

				// Compute per-frame statistics
				decoded := decodeBuf[:n*channels]
				var decEnergy, refEnergy float64
				for j := 0; j < len(decoded) && j < tc.frameSize; j++ {
					decEnergy += float64(decoded[j]) * float64(decoded[j])
				}
				for j := start; j < end; j++ {
					refEnergy += float64(signal[j]) * float64(signal[j])
				}
				decRMS := math.Sqrt(decEnergy / float64(tc.frameSize))
				refRMS := math.Sqrt(refEnergy / float64(tc.frameSize))

				if i >= 3 { // Skip warmup
					t.Logf("Frame %d: pkt=%d bytes, decoded=%d samp, decRMS=%.6f, refRMS=%.6f, ratio=%.1f%%",
						i, len(cp), n, decRMS, refRMS, decRMS/refRMS*100)
				}

				// For NB-10ms, also dump first few TOC bytes
				if i == 5 {
					t.Logf("  TOC byte: 0x%02x", cp[0])
					t.Logf("  Packet bytes: %d", len(cp))
				}
			}
		})
	}
}

// TestNB10msConstantSignal tests with a constant amplitude signal
// to more easily spot gain errors.
func TestNB10msConstantSignal(t *testing.T) {
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

			numFrames := 40
			totalSamples := numFrames * tc.frameSize * channels
			signal := make([]float32, totalSamples)
			for i := range signal {
				ti := float64(i) / float64(sampleRate)
				signal[i] = float32(0.3 * math.Sin(2*math.Pi*300.0*ti))
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

				if i >= 10 { // Skip more warmup for stability
					decoded := decodeBuf[:n*channels]
					for j := range decoded {
						totalDecEnergy += float64(decoded[j]) * float64(decoded[j])
						totalDecCount++
					}
					for j := start; j < end; j++ {
						totalRefEnergy += float64(signal[j]) * float64(signal[j])
						totalRefCount++
					}
				}
			}

			decRMS := math.Sqrt(totalDecEnergy / float64(totalDecCount))
			refRMS := math.Sqrt(totalRefEnergy / float64(totalRefCount))
			t.Logf("Dec RMS=%.6f, Ref RMS=%.6f, ratio=%.1f%%", decRMS, refRMS, decRMS/refRMS*100)
		})
	}
}
