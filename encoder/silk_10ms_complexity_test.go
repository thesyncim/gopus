package encoder

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestSILK10msComplexityEffect tests if the 10ms quality issue is related to
// the complexity level (delayed decision NSQ vs simple NSQ).
func TestSILK10msComplexityEffect(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, complexity := range []int{0, 5, 10} {
		for _, frameSize := range []int{480, 960} {
			fsName := "10ms"
			if frameSize == 960 {
				fsName = "20ms"
			}
			t.Run(fsName+"-c"+fmt.Sprint(complexity), func(t *testing.T) {
				enc := NewEncoder(48000, 1)
				enc.SetMode(ModeSILK)
				enc.SetBandwidth(types.BandwidthWideband)
				enc.SetBitrate(32000)
				enc.SetComplexity(complexity)

				totalSamples := 2 * 48000
				numFrames := totalSamples / frameSize
				origSamples := make([]float32, totalSamples)
				for i := 0; i < totalSamples; i++ {
					tm := float64(i) / 48000.0
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					origSamples[i] = 0.5 * float32(math.Sin(phase))
				}

				var packets [][]byte
				for i := 0; i < numFrames; i++ {
					pcm := make([]float64, frameSize)
					for j := 0; j < frameSize; j++ {
						sampleIdx := i*frameSize + j
						tm := float64(sampleIdx) / 48000.0
						phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
						pcm[j] = 0.5 * math.Sin(phase)
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

				bestSNR := math.Inf(-1)
				bestDelay := 0
				margin := 2000
				for d := -1000; d <= 1000; d++ {
					var sig, noise float64
					count := 0
					for i := margin; i < totalSamples-margin; i++ {
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
							bestDelay = d
						}
					}
				}

				t.Logf("%s complexity=%d: SNR=%.2f dB at delay=%d", fsName, complexity, bestSNR, bestDelay)
			})
		}
	}
}
