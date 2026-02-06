package encoder

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestSILK10msDelaySearch searches for the optimal delay with a very large range
// to determine whether the 10ms quality gap is a delay alignment issue.
func TestSILK10msDelaySearch(t *testing.T) {
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

			// Use 2 seconds of audio for better delay estimation
			numFrames := 2 * 48000 / tc.frameSize
			var packets [][]byte
			var origSamples []float32

			for i := 0; i < numFrames; i++ {
				pcm := make([]float64, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					// Use a more complex signal for better correlation
					pcm[j] = 0.3*math.Sin(2*math.Pi*440*tm) +
						0.2*math.Sin(2*math.Pi*1000*tm) +
						0.1*math.Sin(2*math.Pi*2000*tm)
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

			preSkip := 312
			if len(decoded) > preSkip {
				decoded = decoded[preSkip:]
			}
			t.Logf("Original: %d samples, Decoded: %d samples", len(origSamples), len(decoded))

			// Very large delay search
			bestSNR := math.Inf(-1)
			bestDelay := 0
			maxSearch := 10000

			// First do a coarse search
			for d := -maxSearch; d <= maxSearch; d += 10 {
				snr := computeSNRAtDelay(origSamples, decoded, d)
				if snr > bestSNR {
					bestSNR = snr
					bestDelay = d
				}
			}
			// Then fine search around best
			for d := bestDelay - 20; d <= bestDelay+20; d++ {
				snr := computeSNRAtDelay(origSamples, decoded, d)
				if snr > bestSNR {
					bestSNR = snr
					bestDelay = d
				}
			}

			t.Logf("Best SNR=%.2f dB at delay=%d samples (%.2f ms)",
				bestSNR, bestDelay, float64(bestDelay)/48.0)

			// Also report SNR at compliance test's maxDelay boundary
			for _, testDelay := range []int{-2000, -1000, 0, 1000, 2000} {
				snr := computeSNRAtDelay(origSamples, decoded, testDelay)
				t.Logf("  delay=%d: SNR=%.2f dB", testDelay, snr)
			}

			// Check if the decoded waveform has the right overall energy
			var decEnergy float64
			for i := 1000; i < len(decoded)-1000; i++ {
				decEnergy += float64(decoded[i]) * float64(decoded[i])
			}
			decRMS := math.Sqrt(decEnergy / float64(len(decoded)-2000))

			var origEnergy float64
			for i := 1000; i < len(origSamples)-1000; i++ {
				origEnergy += float64(origSamples[i]) * float64(origSamples[i])
			}
			origRMS := math.Sqrt(origEnergy / float64(len(origSamples)-2000))

			t.Logf("RMS: original=%.4f decoded=%.4f ratio=%.2f%%", origRMS, decRMS, decRMS/origRMS*100)
		})
	}
}

func computeSNRAtDelay(orig, decoded []float32, delay int) float64 {
	var sig, noise float64
	margin := 500
	count := 0
	for i := margin; i < len(orig)-margin; i++ {
		di := i + delay
		if di >= margin && di < len(decoded)-margin {
			ref := float64(orig[i])
			dec := float64(decoded[di])
			sig += ref * ref
			n := dec - ref
			noise += n * n
			count++
		}
	}
	if count < 1000 || sig <= 0 || noise <= 0 {
		return math.Inf(-1)
	}
	return 10 * math.Log10(sig/noise)
}
