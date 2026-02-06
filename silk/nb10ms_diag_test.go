package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestNB10msDiagnostic isolates the NB 10ms amplitude inflation.
// It encodes at native SILK rate (8kHz for NB) and decodes there too,
// comparing raw samples at every stage: encoder input, native decoded output,
// and resampled output.
func TestNB10msDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bw     Bandwidth
		subfrL int
		nSF    int
	}{
		{"NB-10ms", BandwidthNarrowband, 40, 2},
		{"NB-20ms", BandwidthNarrowband, 40, 4},
		{"WB-10ms", BandwidthWideband, 80, 2},
		{"WB-20ms", BandwidthWideband, 80, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := GetBandwidthConfig(tc.bw)
			fsKHz := cfg.SampleRate / 1000
			frameSamples := tc.nSF * tc.subfrL

			enc := NewEncoder(tc.bw)
			enc.SetBitrate(32000)

			dec := NewDecoder()
			resampler := dec.GetResampler(tc.bw)

			numFrames := 50
			totalSamples := numFrames * frameSamples

			// Generate chirp signal at native SILK rate
			pcm := make([]float32, totalSamples+frameSamples)
			for i := range pcm {
				ti := float64(i) / float64(cfg.SampleRate)
				freq := 200.0 + 1800.0*ti
				pcm[i] = float32(0.5 * math.Sin(2*math.Pi*freq*ti))
			}

			// Track RMS at each stage
			var inputEnergy, nativeDecodedEnergy, resampledEnergy float64
			var inputCount, nativeDecodedCount, resampledCount int

			// Warmup: 5 frames
			for i := 0; i < 5; i++ {
				start := i * frameSamples
				end := start + frameSamples
				pkt := enc.EncodeFrame(pcm[start:end], nil, true)
				if len(pkt) == 0 {
					continue
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				frameSizeAt48k := frameSamples * 48000 / cfg.SampleRate
				_, _ = dec.Decode(cp, tc.bw, frameSizeAt48k, true)
			}

			// Measure: frames 5 onwards
			for i := 5; i < numFrames; i++ {
				start := i * frameSamples
				end := start + frameSamples

				// Accumulate input energy
				for j := start; j < end; j++ {
					v := float64(pcm[j])
					inputEnergy += v * v
					inputCount++
				}

				pkt := enc.EncodeFrame(pcm[start:end], nil, true)
				if len(pkt) == 0 {
					t.Logf("Frame %d: empty packet", i)
					continue
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)

				// Decode to native int16 samples (bypassing resampler)
				rd := &rangecoding.Decoder{}
				rd.Init(cp)

				// Read header
				headerICDF := []uint16{uint16(256 - (256 >> 2)), 0}
				header := rd.DecodeICDF16(headerICDF, 8)
				vadFlag := (header>>1)&1 != 0

				duration := FrameDurationFromTOC(frameSamples * 48000 / cfg.SampleRate)

				nativeSamples, err := dec.decodeFrameRawInt16(rd, tc.bw, duration, vadFlag)
				if err != nil {
					t.Logf("Frame %d: decode error: %v", i, err)
					continue
				}

				// Accumulate native decoded energy
				for _, s := range nativeSamples {
					v := float64(s) / 32768.0
					nativeDecodedEnergy += v * v
					nativeDecodedCount++
				}

				// Now resample native -> 48kHz
				resamplerInput := dec.BuildMonoResamplerInputInt16(nativeSamples)
				outBuf := make([]float32, frameSamples*48000/cfg.SampleRate)
				n := resampler.ProcessInt16Into(resamplerInput, outBuf)

				// Accumulate resampled energy
				for j := 0; j < n; j++ {
					v := float64(outBuf[j])
					resampledEnergy += v * v
					resampledCount++
				}
			}

			inputRMS := math.Sqrt(inputEnergy / float64(inputCount))
			nativeDecodedRMS := math.Sqrt(nativeDecodedEnergy / float64(nativeDecodedCount))
			resampledRMS := math.Sqrt(resampledEnergy / float64(resampledCount))

			t.Logf("Input RMS:          %.6f (at native %dkHz rate)", inputRMS, fsKHz)
			t.Logf("Native decoded RMS: %.6f (at native %dkHz rate)", nativeDecodedRMS, fsKHz)
			t.Logf("Resampled RMS:      %.6f (at 48kHz)", resampledRMS)
			t.Logf("Native/Input ratio: %.1f%%", nativeDecodedRMS/inputRMS*100)
			t.Logf("Resampled/Input:    %.1f%%", resampledRMS/inputRMS*100)
		})
	}
}

// TestNB10msResamplerDiag tests whether the NB upsampling resampler (8kHz->48kHz)
// introduces amplitude inflation on its own.
func TestNB10msResamplerDiag(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fsIn    int
		nFrames int
	}{
		{"NB-10ms-80samp", 8000, 10},
		{"NB-20ms-160samp", 8000, 10},
		{"WB-10ms-160samp", 16000, 10},
		{"WB-20ms-320samp", 16000, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsInKHz := tc.fsIn / 1000
			var frameSamples int
			if tc.name[3:6] == "10m" {
				frameSamples = fsInKHz * 10
			} else {
				frameSamples = fsInKHz * 20
			}

			resampler := NewLibopusResampler(tc.fsIn, 48000)

			// Generate test signal at native rate
			totalSamples := tc.nFrames * frameSamples
			signal := make([]int16, totalSamples)
			for i := range signal {
				ti := float64(i) / float64(tc.fsIn)
				signal[i] = int16(16384.0 * math.Sin(2*math.Pi*440.0*ti))
			}

			var inputEnergy, outputEnergy float64
			var inputCount, outputCount int

			for f := 0; f < tc.nFrames; f++ {
				start := f * frameSamples
				end := start + frameSamples
				frame := signal[start:end]

				for _, s := range frame {
					v := float64(s) / 32768.0
					inputEnergy += v * v
					inputCount++
				}

				outBuf := make([]float32, frameSamples*48000/tc.fsIn)
				n := resampler.ProcessInt16Into(frame, outBuf)
				for j := 0; j < n; j++ {
					v := float64(outBuf[j])
					outputEnergy += v * v
					outputCount++
				}
			}

			inputRMS := math.Sqrt(inputEnergy / float64(inputCount))
			outputRMS := math.Sqrt(outputEnergy / float64(outputCount))
			ratio := outputRMS / inputRMS * 100
			t.Logf("Resampler %dHz->48000Hz: input RMS=%.6f, output RMS=%.6f, ratio=%.1f%%",
				tc.fsIn, inputRMS, outputRMS, ratio)
			if math.Abs(ratio-100) > 5 {
				t.Errorf("Resampler amplitude distortion: expected ~100%%, got %.1f%%", ratio)
			}
		})
	}
}

// TestNB10msEncoderResamplerDiag tests the encoder downsampler (48kHz->8kHz) independently.
func TestNB10msEncoderResamplerDiag(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fsOut     int
		inputMs   int
	}{
		{"NB-10ms-480samp", 8000, 10},
		{"NB-20ms-960samp", 8000, 20},
		{"WB-10ms-480samp", 16000, 10},
		{"WB-20ms-960samp", 16000, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputSamples := 48 * tc.inputMs // 48kHz * ms
			outputSamples := (tc.fsOut / 1000) * tc.inputMs

			resampler := NewDownsamplingResampler(48000, tc.fsOut)

			nFrames := 20
			totalInputSamples := nFrames * inputSamples

			// Generate signal at 48kHz
			signal48k := make([]float32, totalInputSamples)
			for i := range signal48k {
				ti := float64(i) / 48000.0
				signal48k[i] = float32(0.5 * math.Sin(2*math.Pi*440.0*ti))
			}

			var inputEnergy, outputEnergy float64
			var inputCount, outputCount int

			for f := 0; f < nFrames; f++ {
				start := f * inputSamples
				end := start + inputSamples
				frame := signal48k[start:end]

				for _, s := range frame {
					inputEnergy += float64(s) * float64(s)
					inputCount++
				}

				out := resampler.Process(frame)
				if len(out) != outputSamples {
					t.Fatalf("Frame %d: expected %d output samples, got %d", f, outputSamples, len(out))
				}

				for _, s := range out {
					outputEnergy += float64(s) * float64(s)
					outputCount++
				}
			}

			inputRMS := math.Sqrt(inputEnergy / float64(inputCount))
			outputRMS := math.Sqrt(outputEnergy / float64(outputCount))
			ratio := outputRMS / inputRMS * 100
			t.Logf("Resampler 48kHz->%dHz: input RMS=%.6f, output RMS=%.6f, ratio=%.1f%%",
				tc.fsOut, inputRMS, outputRMS, ratio)
			if math.Abs(ratio-100) > 5 {
				t.Errorf("Resampler amplitude distortion: expected ~100%%, got %.1f%%", ratio)
			}
		})
	}
}

// TestNB10msEncodeDecodeNativeRate tests encode+decode at native SILK rate
// without any resampling, to isolate whether the SILK codec itself inflates amplitude.
func TestNB10msEncodeDecodeNativeRate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bw     Bandwidth
		nSF    int
	}{
		{"NB-10ms", BandwidthNarrowband, 2},
		{"NB-20ms", BandwidthNarrowband, 4},
		{"WB-10ms", BandwidthWideband, 2},
		{"WB-20ms", BandwidthWideband, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := GetBandwidthConfig(tc.bw)
			subfrSamples := cfg.SubframeSamples
			frameSamples := tc.nSF * subfrSamples

			enc := NewEncoder(tc.bw)
			enc.SetBitrate(32000)

			numFrames := 50
			totalSamples := (numFrames + 1) * frameSamples

			// Generate signal at native rate
			pcm := make([]float32, totalSamples)
			for i := range pcm {
				ti := float64(i) / float64(cfg.SampleRate)
				freq := 200.0 + 1800.0*ti
				pcm[i] = float32(0.5 * math.Sin(2*math.Pi*freq*ti))
			}

			// Encode all frames
			packets := make([][]byte, numFrames)
			for i := 0; i < numFrames; i++ {
				start := i * frameSamples
				end := start + frameSamples
				pkt := enc.EncodeFrame(pcm[start:end], nil, true)
				if len(pkt) > 0 {
					packets[i] = make([]byte, len(pkt))
					copy(packets[i], pkt)
				}
			}

			// Decode all frames and measure
			dec := NewDecoder()
			var inputEnergy, decodedEnergy float64
			var inputCount, decodedCount int

			for i := 5; i < numFrames; i++ { // skip warmup
				if len(packets[i]) == 0 {
					continue
				}

				// Input energy for this frame
				start := i * frameSamples
				end := start + frameSamples
				for j := start; j < end; j++ {
					v := float64(pcm[j])
					inputEnergy += v * v
					inputCount++
				}

				// Decode to native int16
				rd := &rangecoding.Decoder{}
				rd.Init(packets[i])

				headerICDF := []uint16{uint16(256 - (256 >> 2)), 0}
				header := rd.DecodeICDF16(headerICDF, 8)
				vadFlag := (header>>1)&1 != 0

				duration := FrameDurationFromTOC(frameSamples * 48000 / cfg.SampleRate)
				nativeSamples, err := dec.decodeFrameRawInt16(rd, tc.bw, duration, vadFlag)
				if err != nil {
					t.Logf("Frame %d: decode error: %v", i, err)
					continue
				}

				for _, s := range nativeSamples {
					v := float64(s) / 32768.0
					decodedEnergy += v * v
					decodedCount++
				}

				// Log individual frame details for NB-10ms
				if tc.name == "NB-10ms" && i < 10 {
					frameRMS := 0.0
					for _, s := range nativeSamples {
						v := float64(s) / 32768.0
						frameRMS += v * v
					}
					frameRMS = math.Sqrt(frameRMS / float64(len(nativeSamples)))

					inputFrameRMS := 0.0
					for j := start; j < end; j++ {
						v := float64(pcm[j])
						inputFrameRMS += v * v
					}
					inputFrameRMS = math.Sqrt(inputFrameRMS / float64(end-start))

					t.Logf("Frame %d: input RMS=%.6f, decoded RMS=%.6f, ratio=%.1f%%, native samples=%d",
						i, inputFrameRMS, frameRMS, frameRMS/inputFrameRMS*100, len(nativeSamples))
				}
			}

			inputRMS := math.Sqrt(inputEnergy / float64(inputCount))
			decodedRMS := math.Sqrt(decodedEnergy / float64(decodedCount))
			t.Logf("Input RMS: %.6f, Decoded RMS: %.6f, ratio: %.1f%%",
				inputRMS, decodedRMS, decodedRMS/inputRMS*100)

			fmt.Printf("%s: native rate encode->decode ratio = %.1f%%\n",
				tc.name, decodedRMS/inputRMS*100)
		})
	}
}
