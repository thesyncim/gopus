package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestSILK10msQualityDebug traces the complete Opus encoder path for 10ms vs 20ms
// to identify where the quality gap originates.
func TestSILK10msQualityDebug(t *testing.T) {
	// For each frame size, encode with Opus encoder, decode with SILK decoder, measure SNR
	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		silkBW    silk.Bandwidth
		frameSize int
		bitrate   int
	}{
		{"WB-10ms-32k", types.BandwidthWideband, silk.BandwidthWideband, 480, 32000},
		{"WB-20ms-32k", types.BandwidthWideband, silk.BandwidthWideband, 960, 32000},
		{"NB-10ms-32k", types.BandwidthNarrowband, silk.BandwidthNarrowband, 480, 32000},
		{"NB-20ms-32k", types.BandwidthNarrowband, silk.BandwidthNarrowband, 960, 32000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(tc.bitrate)

			dec := silk.NewDecoder()

			numFrames := 30
			var allOriginal []float64
			var allDecoded []float32

			for i := 0; i < numFrames; i++ {
				pcm := make([]float64, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				allOriginal = append(allOriginal, pcm...)

				pkt, err := enc.Encode(pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("Encode error at frame %d: %v", i, err)
				}
				if pkt == nil || len(pkt) < 2 {
					t.Logf("Frame %d: nil or short packet", i)
					// Still add zeros to keep alignment
					zeros := make([]float32, tc.frameSize)
					allDecoded = append(allDecoded, zeros...)
					continue
				}

				// Parse TOC byte to get frame info
				tocByte := pkt[0]
				config := tocByte >> 3
				silkData := pkt[1:]

				if i < 5 || i == 10 || i == 20 {
					t.Logf("Frame %d: pktLen=%d config=%d silkDataLen=%d",
						i, len(pkt), config, len(silkData))
				}

				out, err := dec.Decode(silkData, tc.silkBW, tc.frameSize, true)
				if err != nil {
					t.Logf("Frame %d: decode error: %v", i, err)
					zeros := make([]float32, tc.frameSize)
					allDecoded = append(allDecoded, zeros...)
					continue
				}
				allDecoded = append(allDecoded, out...)
			}

			// Find best delay alignment and compute SNR
			bestSNR := math.Inf(-1)
			bestDelay := 0
			maxSearch := 2000
			origF32 := make([]float32, len(allOriginal))
			for i, v := range allOriginal {
				origF32[i] = float32(v)
			}

			for d := -maxSearch; d <= maxSearch; d++ {
				var sig, noise float64
				margin := 500
				count := 0
				for i := margin; i < len(origF32)-margin; i++ {
					di := i + d
					if di >= margin && di < len(allDecoded)-margin {
						ref := float64(origF32[i])
						dec := float64(allDecoded[di])
						sig += ref * ref
						n := dec - ref
						noise += n * n
						count++
					}
				}
				if count > 0 && sig > 0 && noise > 0 {
					snr := 10 * math.Log10(sig/noise)
					if snr > bestSNR {
						bestSNR = snr
						bestDelay = d
					}
				}
			}

			t.Logf("Best SNR=%.2f dB at delay=%d (total orig=%d decoded=%d)",
				bestSNR, bestDelay, len(origF32), len(allDecoded))

			// Also compute SNR at delay=0 to see baseline
			{
				var sig, noise float64
				margin := 500
				count := 0
				for i := margin; i < len(origF32)-margin && i < len(allDecoded)-margin; i++ {
					ref := float64(origF32[i])
					dec := float64(allDecoded[i])
					sig += ref * ref
					n := dec - ref
					noise += n * n
					count++
				}
				if count > 0 && sig > 0 && noise > 0 {
					snr0 := 10 * math.Log10(sig/noise)
					t.Logf("SNR at delay=0: %.2f dB (%d samples)", snr0, count)
				}
			}
		})
	}
}

// TestSILK10msDirectVsOpusPath compares SILK encoding directly vs through Opus encoder path
func TestSILK10msDirectVsOpusPath(t *testing.T) {
	silkBW := silk.BandwidthWideband
	cfg := silk.GetBandwidthConfig(silkBW)

	for _, tc := range []struct {
		name      string
		frameSize int // at 48kHz
	}{
		{"10ms", 480},
		{"20ms", 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Direct SILK encoding at 16kHz
			directEnc := silk.NewEncoder(silkBW)
			directEnc.SetBitrate(32000)
			directDec := silk.NewDecoder()

			silkFrameSamples := tc.frameSize * cfg.SampleRate / 48000
			numFrames := 30

			pcm16k := make([]float32, numFrames*silkFrameSamples+silkFrameSamples)
			for i := range pcm16k {
				pcm16k[i] = float32(0.5 * math.Sin(2*math.Pi*440.0*float64(i)/float64(cfg.SampleRate)))
			}

			var directRMS []float64
			for frame := 0; frame < numFrames; frame++ {
				start := frame * silkFrameSamples
				end := start + silkFrameSamples
				pkt := directEnc.EncodeFrame(pcm16k[start:end], nil, true)
				if pkt == nil {
					continue
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				out, err := directDec.Decode(cp, silkBW, tc.frameSize, true)
				if err != nil {
					continue
				}
				var energy float64
				for _, s := range out {
					energy += float64(s) * float64(s)
				}
				r := math.Sqrt(energy / float64(len(out)))
				directRMS = append(directRMS, r)
			}

			// Opus encoder path at 48kHz
			opusEnc := NewEncoder(48000, 1)
			opusEnc.SetMode(ModeSILK)
			opusEnc.SetBandwidth(types.BandwidthWideband)
			opusEnc.SetBitrate(32000)
			opusDec := silk.NewDecoder()

			var opusRMS []float64
			for frame := 0; frame < numFrames; frame++ {
				pcm48k := make([]float64, tc.frameSize)
				for j := range pcm48k {
					sampleIdx := frame*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm48k[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := opusEnc.Encode(pcm48k, tc.frameSize)
				if err != nil || pkt == nil || len(pkt) < 2 {
					continue
				}
				out, err := opusDec.Decode(pkt[1:], silkBW, tc.frameSize, true)
				if err != nil {
					continue
				}
				var energy float64
				for _, s := range out {
					energy += float64(s) * float64(s)
				}
				r := math.Sqrt(energy / float64(len(out)))
				opusRMS = append(opusRMS, r)
			}

			expected := 0.5 / math.Sqrt(2)
			t.Logf("Expected RMS: %.4f", expected)
			if len(directRMS) > 5 {
				avg := 0.0
				for _, v := range directRMS[5:] {
					avg += v
				}
				avg /= float64(len(directRMS) - 5)
				t.Logf("Direct SILK avg RMS (after warmup): %.4f (ratio=%.2f%%)", avg, avg/expected*100)
			}
			if len(opusRMS) > 5 {
				avg := 0.0
				for _, v := range opusRMS[5:] {
					avg += v
				}
				avg /= float64(len(opusRMS) - 5)
				t.Logf("Opus path avg RMS (after warmup): %.4f (ratio=%.2f%%)", avg, avg/expected*100)
			}
		})
	}
}
