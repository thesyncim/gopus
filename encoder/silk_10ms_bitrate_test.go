package encoder

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestSILK10msBitrateStudy measures SNR vs bitrate for 10ms and 20ms to see
// if the quality gap narrows at higher bitrates.
func TestSILK10msBitrateStudy(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Skip("opusdec not found")
	}

	for _, bitrate := range []int{16000, 24000, 32000, 48000, 64000} {
		for _, frameSize := range []int{480, 960} {
			fsName := "10ms"
			if frameSize == 960 {
				fsName = "20ms"
			}
			name := fsName + "-" + itoa(bitrate/1000) + "k"
			t.Run(name, func(t *testing.T) {
				enc := NewEncoder(48000, 1)
				enc.SetMode(ModeSILK)
				enc.SetBandwidth(types.BandwidthWideband)
				enc.SetBitrate(bitrate)

				numFrames := 2 * 48000 / frameSize
				var packets [][]byte
				var origSamples []float32

				for i := 0; i < numFrames; i++ {
					pcm := make([]float64, frameSize)
					for j := 0; j < frameSize; j++ {
						sampleIdx := i*frameSize + j
						tm := float64(sampleIdx) / 48000.0
						freq := 200.0 + 900.0*tm
						phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
						pcm[j] = 0.5 * math.Sin(phase)
						_ = freq
					}
					for _, v := range pcm {
						origSamples = append(origSamples, float32(v))
					}
					pkt, err := enc.Encode(pcm, frameSize)
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
				writeTestOgg(&oggBuf, packets, 1, 48000, frameSize, 312)
				decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

				preSkip := 312
				if len(decoded) > preSkip {
					decoded = decoded[preSkip:]
				}

				// Find best SNR
				bestSNR := math.Inf(-1)
				for d := -1000; d <= 1000; d++ {
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
						}
					}
				}

				// Compute avg packet size
				totalBytes := 0
				for _, p := range packets {
					totalBytes += len(p)
				}
				avgPktSize := float64(totalBytes) / float64(len(packets))
				effectiveBitrate := avgPktSize * 8 * float64(48000) / float64(frameSize)

				t.Logf("bitrate=%dk frameSize=%s SNR=%.2f dB avgPkt=%.1f effectiveBps=%.0f",
					bitrate/1000, fsName, bestSNR, avgPktSize, effectiveBitrate)
			})
		}
	}
}
