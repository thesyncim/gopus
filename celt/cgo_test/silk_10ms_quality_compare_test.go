//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"
)

// TestLibopusSILK10msVs20msQuality compares libopus encoding quality at 10ms vs 20ms
// to establish whether the quality gap is inherent or gopus-specific.
func TestLibopusSILK10msVs20msQuality(t *testing.T) {
	for _, bitrate := range []int{16000, 32000, 64000} {
		for _, frameSize := range []int{480, 960} {
			fsName := "10ms"
			if frameSize == 960 {
				fsName = "20ms"
			}
			t.Run(fsName+"-"+itoa10(bitrate/1000)+"k", func(t *testing.T) {
				enc, err := NewLibopusEncoder(48000, 1, OpusApplicationVoIP)
				if err != nil || enc == nil {
					t.Fatal("failed to create libopus encoder")
				}
				defer enc.Destroy()

				enc.SetBitrate(bitrate)
				enc.SetBandwidth(OpusBandwidthWideband)
				enc.SetVBR(true)
				enc.SetSignal(OpusSignalVoice)

				dec, err := NewLibopusDecoder(48000, 1)
				if err != nil || dec == nil {
					t.Fatal("failed to create libopus decoder")
				}
				defer dec.Destroy()

				// Generate chirp signal (2 seconds)
				numFrames := 2 * 48000 / frameSize
				totalSamples := numFrames * frameSize
				origSamples := make([]float32, totalSamples)
				for i := 0; i < totalSamples; i++ {
					tm := float64(i) / 48000.0
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					origSamples[i] = 0.5 * float32(math.Sin(phase))
				}

				// Encode and decode
				decodedSamples := make([]float32, 0, totalSamples+5000)
				totalBytes := 0
				for f := 0; f < numFrames; f++ {
					start := f * frameSize
					end := start + frameSize
					pcm := origSamples[start:end]

					pkt, n := enc.EncodeFloat(pcm, frameSize)
					if n <= 0 {
						t.Fatalf("encode frame %d failed: %d", f, n)
					}
					totalBytes += n

					decoded, dn := dec.DecodeFloat(pkt, 5760)
					if dn <= 0 {
						t.Fatalf("decode frame %d failed: %d", f, dn)
					}
					decodedSamples = append(decodedSamples, decoded...)
				}

				avgPktSize := float64(totalBytes) / float64(numFrames)
				effectiveBps := avgPktSize * 8 * float64(48000) / float64(frameSize)

				// Find best SNR
				bestSNR := math.Inf(-1)
				bestDelay := 0
				margin := 2000
				for d := -1000; d <= 1000; d++ {
					var sig, noise float64
					count := 0
					for i := margin; i < totalSamples-margin; i++ {
						di := i + d
						if di >= margin && di < len(decodedSamples)-margin {
							ref := float64(origSamples[i])
							dec := float64(decodedSamples[di])
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
							bestDelay = d
						}
					}
				}

				t.Logf("libopus %s @%dk: SNR=%.2f dB delay=%d avgPkt=%.1f effectiveBps=%.0f",
					fsName, bitrate/1000, bestSNR, bestDelay, avgPktSize, effectiveBps)
			})
		}
	}
}

func itoa10(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
