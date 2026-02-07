package testvectors

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestNarrowband10msChirpNoBurst guards against NB 10ms frame bursts that
// previously produced clipped/over-amplified output on chirps.
func TestNarrowband10msChirpNoBurst(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 480 // 10 ms at 48 kHz
		bitrate    = 32000
	)

	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	signal := make([]float32, totalSamples)
	for i := 0; i < totalSamples; i++ {
		ti := float64(i) / float64(sampleRate)
		freq := 200.0 + 1800.0*ti
		signal[i] = float32(0.5 * math.Sin(2*math.Pi*freq*ti))
	}

	enc := encoder.NewEncoder(sampleRate, channels)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthNarrowband)
	enc.SetBitrate(bitrate)

	dec, err := gopus.NewDecoder(gopus.DecoderConfig{
		SampleRate: sampleRate,
		Channels:   channels,
	})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	decodeBuf := make([]float32, 5760)
	decoded := make([]float32, 0, totalSamples+5760)

	maxFrameRatio := 0.0
	for i := 0; i < numFrames; i++ {
		start := i * frameSize * channels
		end := start + frameSize*channels
		pkt, err := enc.Encode(float32ToFloat64(signal[start:end]), frameSize)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", i, err)
		}
		n, err := dec.Decode(pkt, decodeBuf)
		if err != nil {
			t.Fatalf("Decode frame %d: %v", i, err)
		}

		frameDecoded := decodeBuf[:n*channels]
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
			if refRMS > 0 {
				ratio := decRMS / refRMS
				if ratio > maxFrameRatio {
					maxFrameRatio = ratio
				}
			}
		}

		decoded = append(decoded, frameDecoded...)
	}

	var decEnergy, refEnergy float64
	for _, s := range decoded {
		decEnergy += float64(s) * float64(s)
	}
	for _, s := range signal {
		refEnergy += float64(s) * float64(s)
	}
	decRMS := math.Sqrt(decEnergy / float64(len(decoded)))
	refRMS := math.Sqrt(refEnergy / float64(len(signal)))
	ratio := decRMS / refRMS

	clipped := 0
	for _, s := range decoded {
		if s > 0.95 || s < -0.95 {
			clipped++
		}
	}
	clippedPct := 100 * float64(clipped) / float64(len(decoded))

	if ratio < 0.90 || ratio > 1.10 {
		t.Fatalf("overall RMS ratio %.3f out of range [0.90, 1.10]", ratio)
	}
	if maxFrameRatio > 1.35 {
		t.Fatalf("frame RMS burst detected: max frame ratio %.3f > 1.35", maxFrameRatio)
	}
	if clippedPct > 1.0 {
		t.Fatalf("clipping regression: %.2f%% samples exceed |0.95|", clippedPct)
	}
}
