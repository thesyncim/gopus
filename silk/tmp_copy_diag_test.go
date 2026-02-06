package silk

import (
	"fmt"
	"math"
	"testing"
)

// TestDiagnose10msVs20ms traces key encoding parameters for 10ms vs 20ms
// to identify the root cause of the quality gap.
func TestDiagnose10msVs20ms(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bw     Bandwidth
		nSubfr int // 2 for 10ms, 4 for 20ms
	}{
		{"WB-10ms", BandwidthWideband, 2},
		{"WB-20ms", BandwidthWideband, 4},
		{"NB-10ms", BandwidthNarrowband, 2},
		{"NB-20ms", BandwidthNarrowband, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := GetBandwidthConfig(tc.bw)
			subfrSamples := cfg.SubframeSamples
			frameSamples := tc.nSubfr * subfrSamples
			fsKHz := cfg.SampleRate / 1000

			enc := NewEncoder(tc.bw)
			enc.SetBitrate(32000)

			// Generate sine wave
			totalSamples := 20 * frameSamples // 20 frames
			pcm := make([]float32, totalSamples+frameSamples)
			for i := range pcm {
				pcm[i] = float32(0.5 * math.Sin(2*math.Pi*440.0*float64(i)/float64(cfg.SampleRate)))
			}

			dec := NewDecoder()

			for frame := 0; frame < 20; frame++ {
				start := frame * frameSamples
				end := start + frameSamples
				framePCM := pcm[start:end]

				pkt := enc.EncodeFrame(framePCM, nil, true)
				if pkt == nil || len(pkt) == 0 {
					continue
				}

				// Check maxBits, snrDBQ7, gains
				payloadMs := (frameSamples * 1000) / cfg.SampleRate
				computedMaxBits := 0
				if enc.targetRateBps > 0 && payloadMs > 0 {
					computedMaxBits = enc.targetRateBps * payloadMs / 1000
				}

				if frame >= 3 && frame <= 6 {
					t.Logf("Frame %d: pktLen=%d payloadMs=%d maxBits=%d(set=%d) snrDBQ7=%d targetRate=%d",
						frame, len(pkt), payloadMs, computedMaxBits, enc.maxBits,
						enc.snrDBQ7, enc.lastControlTargetRateBps)
					t.Logf("  nBitsExceeded=%d prevGainIdx=%d frameCounter=%d",
						enc.nBitsExceeded, enc.previousGainIndex, enc.frameCounter)
				}

				// Decode
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				frameSizeAt48k := frameSamples * 48000 / cfg.SampleRate
				out, err := dec.Decode(cp, tc.bw, frameSizeAt48k, true)
				if err != nil {
					continue
				}

				if frame >= 3 {
					// Compute signal energy and error
					var sigE, errE float64
					n := len(out)
					if n > len(framePCM)*48000/cfg.SampleRate {
						n = len(framePCM) * 48000 / cfg.SampleRate
					}
					// Resample original to 48kHz for comparison
					ratio := float64(48000) / float64(cfg.SampleRate)
					for j := 0; j < n; j++ {
						origIdx := float64(j) / ratio
						origI := int(origIdx)
						if origI >= len(framePCM)-1 {
							origI = len(framePCM) - 2
						}
						if origI < 0 {
							origI = 0
						}
						frac := origIdx - float64(origI)
						origVal := float64(framePCM[origI])*(1-frac) + float64(framePCM[origI+1])*frac
						decoded := float64(out[j])
						sigE += origVal * origVal
						errE += (decoded - origVal) * (decoded - origVal)
					}
					if errE > 0 {
						snr := 10 * math.Log10(sigE/errE)
						_ = snr
					}

					// Check peak
					var maxAbs float64
					for _, s := range out {
						v := math.Abs(float64(s))
						if v > maxAbs {
							maxAbs = v
						}
					}
					if frame == 5 {
						t.Logf("  Peak=%.4f nSamples=%d", maxAbs, len(out))
					}
				}
			}

			// Now check a key difference: what the NLSF interpolation does for 10ms vs 20ms
			t.Logf("fsKHz=%d subfrSamples=%d nSubfr=%d", fsKHz, subfrSamples, tc.nSubfr)
		})
	}
}

// TestDiagnoseNLSFInterpFor10ms checks if NLSF interpolation is causing issues for 10ms
func TestDiagnoseNLSFInterpFor10ms(t *testing.T) {
	cfg := GetBandwidthConfig(BandwidthWideband)
	subfrSamples := cfg.SubframeSamples
	frameSamples10 := 2 * subfrSamples // 10ms = 2 subframes
	frameSamples20 := 4 * subfrSamples // 20ms = 4 subframes

	// Generate test signal
	pcm := make([]float32, 20*frameSamples20)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*440.0*float64(i)/float64(cfg.SampleRate)))
	}

	// Encode with 10ms frames
	enc10 := NewEncoder(BandwidthWideband)
	enc10.SetBitrate(32000)
	var pkts10 [][]byte
	for frame := 0; frame < 10; frame++ {
		start := frame * frameSamples10
		end := start + frameSamples10
		pkt := enc10.EncodeFrame(pcm[start:end], nil, true)
		if pkt != nil {
			cp := make([]byte, len(pkt))
			copy(cp, pkt)
			pkts10 = append(pkts10, cp)
		}
	}

	// Encode with 20ms frames
	enc20 := NewEncoder(BandwidthWideband)
	enc20.SetBitrate(32000)
	var pkts20 [][]byte
	for frame := 0; frame < 5; frame++ {
		start := frame * frameSamples20
		end := start + frameSamples20
		pkt := enc20.EncodeFrame(pcm[start:end], nil, true)
		if pkt != nil {
			cp := make([]byte, len(pkt))
			copy(cp, pkt)
			pkts20 = append(pkts20, cp)
		}
	}

	t.Logf("10ms: %d packets, sizes: %v", len(pkts10), pktSizes(pkts10))
	t.Logf("20ms: %d packets, sizes: %v", len(pkts20), pktSizes(pkts20))

	// Decode 10ms packets
	dec10 := NewDecoder()
	var rms10 []float64
	for i, pkt := range pkts10 {
		out, err := dec10.Decode(pkt, BandwidthWideband, frameSamples10*48000/cfg.SampleRate, true)
		if err != nil {
			t.Logf("10ms decode frame %d: err=%v", i, err)
			continue
		}
		var energy float64
		for _, s := range out {
			energy += float64(s) * float64(s)
		}
		r := math.Sqrt(energy / float64(len(out)))
		rms10 = append(rms10, r)
	}

	// Decode 20ms packets
	dec20 := NewDecoder()
	var rms20 []float64
	for i, pkt := range pkts20 {
		out, err := dec20.Decode(pkt, BandwidthWideband, frameSamples20*48000/cfg.SampleRate, true)
		if err != nil {
			t.Logf("20ms decode frame %d: err=%v", i, err)
			continue
		}
		var energy float64
		for _, s := range out {
			energy += float64(s) * float64(s)
		}
		r := math.Sqrt(energy / float64(len(out)))
		rms20 = append(rms20, r)
	}

	t.Logf("10ms RMS per frame: %v", fmtF64(rms10))
	t.Logf("20ms RMS per frame: %v", fmtF64(rms20))
	t.Logf("Expected RMS for 0.5 amplitude sine: %.4f", 0.5/math.Sqrt(2))
}

func pktSizes(pkts [][]byte) []int {
	sizes := make([]int, len(pkts))
	for i, p := range pkts {
		sizes[i] = len(p)
	}
	return sizes
}

func fmtF64(vals []float64) string {
	s := "["
	for i, v := range vals {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%.4f", v)
	}
	return s + "]"
}
