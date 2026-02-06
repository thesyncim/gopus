package encoder

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestSILK10msOriginalDelay finds the true delay between original 48kHz input
// and opusdec output for both 10ms and 20ms. This uses a chirp signal which
// gives unambiguous delay measurement (unlike sine waves).
func TestSILK10msOriginalDelay(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Skip("opusdec not found")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			// Use a chirp signal: frequency sweeps from 200 to 2000 Hz over 2 seconds
			// This gives unambiguous delay measurement
			numFrames := 2 * 48000 / tc.frameSize
			var packets [][]byte
			var origSamples []float32

			for i := 0; i < numFrames; i++ {
				pcm := make([]float64, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					// Chirp: freq = 200 + 900*t (200 Hz at t=0, 2000 Hz at t=2)
					freq := 200.0 + 900.0*tm
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					pcm[j] = 0.5 * math.Sin(phase)
					_ = freq
				}
				for _, v := range pcm {
					origSamples = append(origSamples, float32(v))
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

			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)
			decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			// Strip pre-skip
			preSkip := 312
			if len(decoded) > preSkip {
				decoded = decoded[preSkip:]
			}
			t.Logf("Original: %d samples, Decoded: %d samples", len(origSamples), len(decoded))

			// Find best delay using cross-correlation
			bestCorr := float64(-1)
			bestDelay := 0
			maxSearch := 5000

			for d := -maxSearch; d <= maxSearch; d++ {
				var corr, norm1, norm2 float64
				count := 0
				margin := 2000
				for i := margin; i < len(origSamples)-margin; i++ {
					di := i + d
					if di >= margin && di < len(decoded)-margin {
						a := float64(origSamples[i])
						b := float64(decoded[di])
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
			t.Logf("Best correlation=%.6f at delay=%d (%.2f ms)", bestCorr, bestDelay, float64(bestDelay)/48.0)

			// Compute SNR at best delay
			if bestCorr > 0.5 {
				var sig, noise float64
				margin := 2000
				for i := margin; i < len(origSamples)-margin; i++ {
					di := i + bestDelay
					if di >= margin && di < len(decoded)-margin {
						ref := float64(origSamples[i])
						dec := float64(decoded[di])
						sig += ref * ref
						n := dec - ref
						noise += n * n
					}
				}
				if sig > 0 && noise > 0 {
					snr := 10 * math.Log10(sig/noise)
					t.Logf("SNR at best delay: %.2f dB", snr)
				}
			}

			// Also try with maxDelay=4000 (compliance test range)
			bestSNR := math.Inf(-1)
			bestSNRDelay := 0
			for d := -4000; d <= 4000; d++ {
				var sig, noise float64
				margin := 2000
				count := 0
				for i := margin; i < len(origSamples)-margin; i++ {
					di := i + d
					if di >= margin && di < len(decoded)-margin {
						ref := float64(origSamples[i])
						dec := float64(decoded[di])
						sig += ref * ref
						n := dec - ref
						noise += n * n
						count++
					}
				}
				if count > 1000 && sig > 0 && noise > 0 {
					snr := 10 * math.Log10(sig/noise)
					if snr > bestSNR {
						bestSNR = snr
						bestSNRDelay = d
					}
				}
			}
			t.Logf("Best SNR=%.2f dB at delay=%d", bestSNR, bestSNRDelay)

			// Compute energy ratio at best delay
			var origE, decE float64
			margin := 2000
			for i := margin; i < len(origSamples)-margin; i++ {
				di := i + bestDelay
				if di >= margin && di < len(decoded)-margin {
					origE += float64(origSamples[i]) * float64(origSamples[i])
					decE += float64(decoded[di]) * float64(decoded[di])
				}
			}
			t.Logf("Energy ratio at best delay: %.1f%%", math.Sqrt(decE/origE)*100)
		})
	}
}
