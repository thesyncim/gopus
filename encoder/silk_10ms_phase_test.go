package encoder

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestSILK10msPhaseAnalysis compares the internal decoder output vs opusdec output
// sample-by-sample to identify phase/timing issues specific to 10ms.
func TestSILK10msPhaseAnalysis(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		silkBW    silk.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, silk.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, silk.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			numFrames := 50
			var packets [][]byte

			for i := 0; i < numFrames; i++ {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := enc.Encode(pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			// Decode with internal decoder
			intDec := silk.NewDecoder()
			var internalSamples []float32
			for i, pkt := range packets {
				out, err := intDec.Decode(pkt[1:], tc.silkBW, tc.frameSize, true)
				if err != nil {
					t.Fatalf("internal decode frame %d: %v", i, err)
				}
				internalSamples = append(internalSamples, out...)
			}

			// Decode with opusdec
			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)
			opusSamples := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			preSkip := 312
			if len(opusSamples) > preSkip {
				opusSamples = opusSamples[preSkip:]
			}

			t.Logf("Internal: %d samples, opusdec: %d samples", len(internalSamples), len(opusSamples))

			// Find the best delay between internal and opusdec outputs
			bestCorr := float64(-1)
			bestDelay := 0
			for d := -2000; d <= 2000; d++ {
				var corr, norm1, norm2 float64
				count := 0
				for i := 500; i < len(internalSamples)-500; i++ {
					di := i + d
					if di >= 0 && di < len(opusSamples) {
						a := float64(internalSamples[i])
						b := float64(opusSamples[di])
						corr += a * b
						norm1 += a * a
						norm2 += b * b
						count++
					}
				}
				if count > 1000 && norm1 > 0 && norm2 > 0 {
					c := corr / math.Sqrt(norm1*norm2)
					if c > bestCorr {
						bestCorr = c
						bestDelay = d
					}
				}
			}
			t.Logf("Best correlation=%.4f at delay=%d between internal and opusdec", bestCorr, bestDelay)

			// Now compute per-frame correlation between internal and opusdec
			// to see if there's a frame-to-frame variation
			internalFrameSize := tc.frameSize // at 48kHz rate
			for frame := 2; frame < numFrames-2 && frame < 20; frame++ {
				intStart := frame * internalFrameSize
				intEnd := intStart + internalFrameSize
				if intEnd > len(internalSamples) {
					break
				}

				// Try to find the best matching segment in opusdec output
				bestFrameCorr := float64(-1)
				bestFrameDelay := 0
				for d := bestDelay - 200; d <= bestDelay+200; d++ {
					var corr, norm1, norm2 float64
					for i := intStart; i < intEnd; i++ {
						di := i + d
						if di >= 0 && di < len(opusSamples) {
							a := float64(internalSamples[i])
							b := float64(opusSamples[di])
							corr += a * b
							norm1 += a * a
							norm2 += b * b
						}
					}
					if norm1 > 0 && norm2 > 0 {
						c := corr / math.Sqrt(norm1*norm2)
						if c > bestFrameCorr {
							bestFrameCorr = c
							bestFrameDelay = d
						}
					}
				}

				// Also compute the gain ratio at best delay
				var intE, opE float64
				for i := intStart; i < intEnd; i++ {
					di := i + bestFrameDelay
					if di >= 0 && di < len(opusSamples) {
						intE += float64(internalSamples[i]) * float64(internalSamples[i])
						opE += float64(opusSamples[di]) * float64(opusSamples[di])
					}
				}
				gainRatio := math.Sqrt(opE / intE) * 100

				t.Logf("  Frame %2d: corr=%.4f delay=%d gain=%.1f%%", frame, bestFrameCorr, bestFrameDelay, gainRatio)
			}

			// Check if opusdec output has the right peak amplitude per frame
			t.Logf("Per-frame peak amplitudes from opusdec:")
			for frame := 0; frame < 15 && frame < numFrames; frame++ {
				start := frame * internalFrameSize
				end := start + internalFrameSize
				if end > len(opusSamples) {
					break
				}
				var maxAbs float64
				for i := start; i < end; i++ {
					v := math.Abs(float64(opusSamples[i]))
					if v > maxAbs {
						maxAbs = v
					}
				}
				t.Logf("  opusdec frame %2d: peak=%.4f", frame, maxAbs)
			}
		})
	}
}
