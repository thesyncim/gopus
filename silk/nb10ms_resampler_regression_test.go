package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

func TestNB10msDecoderUpsamplerAmplitudeRegression(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fsIn    int
		frameMs int
	}{
		{"NB-10ms", 8000, 10},
		{"NB-20ms", 8000, 20},
		{"WB-10ms", 16000, 10},
		{"WB-20ms", 16000, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frameSamples := tc.fsIn * tc.frameMs / 1000
			resampler := NewLibopusResampler(tc.fsIn, 48000)

			const nFrames = 12
			var inEnergy, outEnergy float64
			var inCount, outCount int

			for f := 0; f < nFrames; f++ {
				frame := make([]int16, frameSamples)
				for i := range frame {
					sampleIdx := f*frameSamples + i
					ti := float64(sampleIdx) / float64(tc.fsIn)
					frame[i] = int16(16384.0 * math.Sin(2*math.Pi*440.0*ti))
				}
				for _, s := range frame {
					v := float64(s) / 32768.0
					inEnergy += v * v
					inCount++
				}

				out := make([]float32, frameSamples*48000/tc.fsIn)
				n := resampler.ProcessInt16Into(frame, out)
				for i := 0; i < n; i++ {
					v := float64(out[i])
					outEnergy += v * v
					outCount++
				}
			}

			inRMS := math.Sqrt(inEnergy / float64(inCount))
			outRMS := math.Sqrt(outEnergy / float64(outCount))
			ratio := outRMS / inRMS * 100
			if math.Abs(ratio-100) > 5 {
				t.Fatalf("upsampler %d->48000 amplitude ratio=%.1f%% want 100%% +/- 5%%", tc.fsIn, ratio)
			}
		})
	}
}

func TestNB10msEncoderDownsamplerAmplitudeRegression(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fsOut   int
		frameMs int
	}{
		{"NB-10ms", 8000, 10},
		{"NB-20ms", 8000, 20},
		{"WB-10ms", 16000, 10},
		{"WB-20ms", 16000, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputSamples := 48000 * tc.frameMs / 1000
			resampler := NewDownsamplingResampler(48000, tc.fsOut)

			const nFrames = 12
			var inEnergy, outEnergy float64
			var inCount, outCount int

			for f := 0; f < nFrames; f++ {
				frame := make([]float32, inputSamples)
				for i := range frame {
					sampleIdx := f*inputSamples + i
					ti := float64(sampleIdx) / 48000.0
					frame[i] = float32(0.5 * math.Sin(2*math.Pi*440.0*ti))
				}
				for _, s := range frame {
					v := float64(s)
					inEnergy += v * v
					inCount++
				}

				out := resampler.Process(frame)
				for _, s := range out {
					v := float64(s)
					outEnergy += v * v
					outCount++
				}
			}

			inRMS := math.Sqrt(inEnergy / float64(inCount))
			outRMS := math.Sqrt(outEnergy / float64(outCount))
			ratio := outRMS / inRMS * 100
			if math.Abs(ratio-100) > 5 {
				t.Fatalf("downsampler 48000->%d amplitude ratio=%.1f%% want 100%% +/- 5%%", tc.fsOut, ratio)
			}
		})
	}
}

func TestNB10msNativeDecodePathRegression(t *testing.T) {
	for _, tc := range []struct {
		name string
		bw   Bandwidth
		nSF  int
	}{
		{"NB-10ms", BandwidthNarrowband, 2},
		{"NB-20ms", BandwidthNarrowband, 4},
		{"WB-10ms", BandwidthWideband, 2},
		{"WB-20ms", BandwidthWideband, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := GetBandwidthConfig(tc.bw)
			frameSamples := cfg.SubframeSamples * tc.nSF
			frameSizeAt48k := frameSamples * 48000 / cfg.SampleRate

			enc := NewEncoder(tc.bw)
			enc.SetBitrate(32000)
			dec := NewDecoder()
			resampler := dec.GetResampler(tc.bw)

			// Warm up state.
			for f := 0; f < 4; f++ {
				frame := make([]float32, frameSamples)
				for i := range frame {
					sampleIdx := f*frameSamples + i
					ti := float64(sampleIdx) / float64(cfg.SampleRate)
					frame[i] = float32(0.5 * math.Sin(2*math.Pi*440.0*ti))
				}
				pkt := enc.EncodeFrame(frame, nil, true)
				if len(pkt) == 0 {
					t.Fatalf("warmup frame %d: empty packet", f)
				}
				cp := append([]byte(nil), pkt...)
				if _, err := dec.Decode(cp, tc.bw, frameSizeAt48k, true); err != nil {
					t.Fatalf("warmup frame %d: decode error: %v", f, err)
				}
			}

			frame := make([]float32, frameSamples)
			for i := range frame {
				ti := float64(i) / float64(cfg.SampleRate)
				frame[i] = float32(0.5 * math.Sin(2*math.Pi*300.0*ti))
			}
			packet := enc.EncodeFrame(frame, nil, true)
			if len(packet) == 0 {
				t.Fatal("test packet is empty")
			}

			rd := &rangecoding.Decoder{}
			rd.Init(packet)
			headerICDF := []uint16{uint16(256 - (256 >> 2)), 0}
			header := rd.DecodeICDF16(headerICDF, 8)
			vadFlag := (header>>1)&1 != 0
			duration := FrameDurationFromTOC(frameSizeAt48k)

			nativeSamples, err := dec.decodeFrameRawInt16(rd, tc.bw, duration, vadFlag)
			if err != nil {
				t.Fatalf("decodeFrameRawInt16: %v", err)
			}
			if len(nativeSamples) == 0 {
				t.Fatal("decodeFrameRawInt16 returned no samples")
			}

			in := dec.BuildMonoResamplerInputInt16(nativeSamples)
			out := make([]float32, frameSizeAt48k)
			n := resampler.ProcessInt16Into(in, out)
			if n != frameSizeAt48k {
				t.Fatalf("resampler output=%d want %d", n, frameSizeAt48k)
			}

			var inE, outE float64
			for _, s := range frame {
				v := float64(s)
				inE += v * v
			}
			for i := 0; i < n; i++ {
				v := float64(out[i])
				outE += v * v
			}
			inRMS := math.Sqrt(inE / float64(len(frame)))
			outRMS := math.Sqrt(outE / float64(n))
			ratio := outRMS / (inRMS + 1e-12)
			if math.IsNaN(ratio) || ratio < 0.10 || ratio > 3.00 {
				t.Fatalf("native decode path unstable RMS ratio=%0.3f", ratio)
			}
		})
	}
}

func TestNB10msPublicDecodeRegression(t *testing.T) {
	for _, tc := range []struct {
		name string
		bw   Bandwidth
		nSF  int
	}{
		{"NB-10ms", BandwidthNarrowband, 2},
		{"WB-10ms", BandwidthWideband, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := GetBandwidthConfig(tc.bw)
			frameSamples := cfg.SubframeSamples * tc.nSF
			frameSizeAt48k := frameSamples * 48000 / cfg.SampleRate

			enc := NewEncoder(tc.bw)
			enc.SetBitrate(32000)
			dec := NewDecoder()

			pcm := make([]float32, frameSamples)
			for i := range pcm {
				ti := float64(i) / float64(cfg.SampleRate)
				pcm[i] = float32(0.4 * math.Sin(2*math.Pi*220.0*ti))
			}
			packet := enc.EncodeFrame(pcm, nil, true)
			if len(packet) == 0 {
				t.Fatal("empty packet")
			}
			out, err := dec.Decode(packet, tc.bw, frameSizeAt48k, true)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if len(out) != frameSizeAt48k {
				t.Fatalf("decoded samples=%d want %d", len(out), frameSizeAt48k)
			}
		})
	}
}
