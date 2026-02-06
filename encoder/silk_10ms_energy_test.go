package encoder

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestSILK10msEnergyCheck checks the decoded RMS energy from opusdec for 10ms vs 20ms
// with different signals.
func TestSILK10msEnergyCheck(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Skip("opusdec not found")
	}

	signals := []struct {
		name string
		gen  func(int) float64
	}{
		{"sine440", func(i int) float64 {
			return 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000.0)
		}},
		{"multitone", func(i int) float64 {
			t := float64(i) / 48000.0
			return 0.3*math.Sin(2*math.Pi*440*t) +
				0.2*math.Sin(2*math.Pi*1000*t) +
				0.1*math.Sin(2*math.Pi*2000*t)
		}},
	}

	for _, sig := range signals {
		for _, fs := range []int{480, 960} {
			fsName := "10ms"
			if fs == 960 {
				fsName = "20ms"
			}
			t.Run(sig.name+"-"+fsName, func(t *testing.T) {
				enc := NewEncoder(48000, 1)
				enc.SetMode(ModeSILK)
				enc.SetBandwidth(types.BandwidthWideband)
				enc.SetBitrate(32000)

				numFrames := 48000 / fs // 1 second
				var packets [][]byte
				var origEnergy float64

				for i := 0; i < numFrames; i++ {
					pcm := make([]float64, fs)
					for j := 0; j < fs; j++ {
						sampleIdx := i*fs + j
						pcm[j] = sig.gen(sampleIdx)
						origEnergy += pcm[j] * pcm[j]
					}
					pkt, err := enc.Encode(pcm, fs)
					if err != nil {
						t.Fatalf("frame %d: %v", i, err)
					}
					if pkt == nil {
						t.Fatalf("nil frame %d", i)
					}
					cp := make([]byte, len(pkt))
					copy(cp, pkt)
					packets = append(packets, cp)
				}

				origRMS := math.Sqrt(origEnergy / float64(numFrames*fs))

				var oggBuf bytes.Buffer
				writeTestOgg(&oggBuf, packets, 1, 48000, fs, 312)
				decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

				// Strip pre-skip
				preSkip := 312
				if len(decoded) > preSkip {
					decoded = decoded[preSkip:]
				}

				// Compute decoded RMS (skip first and last 1000 samples)
				margin := 1000
				var decEnergy float64
				count := 0
				for i := margin; i < len(decoded)-margin; i++ {
					decEnergy += float64(decoded[i]) * float64(decoded[i])
					count++
				}
				decRMS := math.Sqrt(decEnergy / float64(count))

				ratio := decRMS / origRMS * 100
				t.Logf("OrigRMS=%.4f DecRMS=%.4f Ratio=%.1f%% nDecoded=%d",
					origRMS, decRMS, ratio, len(decoded))

				// Check for energy loss
				if ratio < 90 {
					t.Errorf("Energy loss detected: ratio=%.1f%% (expected >90%%)", ratio)
				}
			})
		}
	}
}
