package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestPitchDetectionWithBuffer tests pitch detection using the pitch analysis buffer.
func TestPitchDetectionWithBuffer(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 320 samples

	// Create encoder
	encoder := NewEncoder(BandwidthWideband)

	// Encode 3 frames to build up history
	for frame := 0; frame < 3; frame++ {
		pcm := make([]float32, frameSamples)
		for i := range pcm {
			tm := float64(frame*frameSamples+i) / float64(config.SampleRate)
			pcm[i] = 0.5 * float32(math.Sin(2*math.Pi*300*tm))
		}

		// Just update the pitch buffer, don't encode
		pitchBufFrameLen := len(pcm)
		if len(encoder.pitchAnalysisBuf) >= pitchBufFrameLen*2 {
			copy(encoder.pitchAnalysisBuf[:pitchBufFrameLen], encoder.pitchAnalysisBuf[pitchBufFrameLen:])
			copy(encoder.pitchAnalysisBuf[pitchBufFrameLen:], pcm)
		}

		// Check buffer state
		var bufSum float64
		for _, s := range encoder.pitchAnalysisBuf {
			bufSum += float64(s) * float64(s)
		}
		bufRMS := math.Sqrt(bufSum / float64(len(encoder.pitchAnalysisBuf)))
		t.Logf("Frame %d: buffer RMS=%.4f", frame, bufRMS)

		// Run pitch detection
		pitchLags := encoder.detectPitch(encoder.pitchAnalysisBuf, 4)
		t.Logf("Frame %d: pitchLags=%v, ltpCorr=%.4f", frame, pitchLags, encoder.pitchState.ltpCorr)

		// On frame 2, trace the Stage 1 correlation manually
		if frame == 2 {
			// Downsample to 4kHz (from 16kHz)
			// First to 8kHz
			frame8kHz := downsampleLowpass(encoder.pitchAnalysisBuf, 2)
			// Then to 4kHz
			frame4kHz := downsampleLowpass(frame8kHz, 2)

			// Apply the same LP filter as pitch detection
			for i := len(frame4kHz) - 1; i > 0; i-- {
				frame4kHz[i] = frame4kHz[i] + frame4kHz[i-1]
			}

			t.Logf("Frame 2: frame4kHz len=%d", len(frame4kHz))

			// Compute Stage 1 correlation at expected lag ~13 (for 300 Hz at 4kHz)
			sfLength8kHz := 40 // 5ms * 8
			targetStart := 80  // sfLength4kHz * 4 = 20 * 4
			minLag4kHz := 8
			expectedLag4kHz := 13

			// Check bounds
			targetIdx := targetStart
			if targetIdx+sfLength8kHz > len(frame4kHz) {
				t.Logf("Frame 2: targetIdx+sfLength8kHz > len(frame4kHz), skipping")
			} else {
				target := frame4kHz[targetIdx : targetIdx+sfLength8kHz]
				var targetEnergy float64
				for _, s := range target {
					targetEnergy += float64(s) * float64(s)
				}
				t.Logf("Frame 2: targetEnergy=%.2f, target[0:5]=%v", targetEnergy, target[:5])

				// Test at expected lag
				for _, testLag := range []int{expectedLag4kHz, minLag4kHz, 20} {
					basisIdx := targetIdx - testLag
					if basisIdx < 0 || basisIdx+sfLength8kHz > len(frame4kHz) {
						t.Logf("Frame 2: lag %d out of bounds", testLag)
						continue
					}
					basis := frame4kHz[basisIdx : basisIdx+sfLength8kHz]

					var xcorr, basisEnergy float64
					for i := 0; i < sfLength8kHz; i++ {
						xcorr += float64(target[i]) * float64(basis[i])
						basisEnergy += float64(basis[i]) * float64(basis[i])
					}
					normalizer := targetEnergy + basisEnergy + float64(sfLength8kHz)*4000.0
					C := 2 * xcorr / normalizer
					t.Logf("Frame 2: lag=%d, xcorr=%.2f, basisE=%.2f, norm=%.2f, C=%.4f",
						testLag, xcorr, basisEnergy, normalizer, C)
				}
			}
		}
	}
}

// TestPitchDetectionDirect tests pitch detection directly on a known signal.
func TestPitchDetectionDirect(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 320 samples
	_ = config.SampleRate / 1000                  // 16 kHz (unused)

	// Generate 300 Hz sine wave
	pcm := make([]float32, frameSamples)
	for i := range pcm {
		tm := float64(i) / float64(config.SampleRate)
		pcm[i] = 0.5 * float32(math.Sin(2*math.Pi*300*tm))
	}

	// Expected pitch lag for 300 Hz at 16 kHz = ~53 samples
	expectedLag := 16000 / 300
	t.Logf("Expected pitch lag: %d samples", expectedLag)

	// Compute autocorrelation at full rate using normalized correlation
	// For a perfect sine, correlation at the period lag should be ~1.0
	var bestCorr float64 = -1
	bestLag := -1
	minLag := 32  // peMinLagMS * 16
	maxLag := 288 // peMaxLagMS * 16 - 1

	for d := minLag; d <= maxLag && d < frameSamples; d++ {
		var xcorr, energy1, energy2 float64
		count := frameSamples - d
		for i := 0; i < count; i++ {
			xcorr += float64(pcm[i]) * float64(pcm[i+d])
			energy1 += float64(pcm[i]) * float64(pcm[i])
			energy2 += float64(pcm[i+d]) * float64(pcm[i+d])
		}
		// Normalized correlation
		normalizer := math.Sqrt(energy1 * energy2)
		if normalizer > 0 {
			corr := xcorr / normalizer
			if corr > bestCorr {
				bestCorr = corr
				bestLag = d
			}
		}
	}

	t.Logf("Normalized autocorrelation: expected lag=%d, found lag=%d, corr=%.4f",
		expectedLag, bestLag, bestCorr)

	// Show correlations at expected lag and multiples
	for _, testLag := range []int{expectedLag, expectedLag * 2, expectedLag * 3, 32, 64} {
		if testLag >= frameSamples {
			continue
		}
		var xcorr, energy1, energy2 float64
		count := frameSamples - testLag
		for i := 0; i < count; i++ {
			xcorr += float64(pcm[i]) * float64(pcm[i+testLag])
			energy1 += float64(pcm[i]) * float64(pcm[i])
			energy2 += float64(pcm[i+testLag]) * float64(pcm[i+testLag])
		}
		normalizer := math.Sqrt(energy1 * energy2)
		corr := 0.0
		if normalizer > 0 {
			corr = xcorr / normalizer
		}
		t.Logf("Correlation at lag=%d: %.4f", testLag, corr)
	}

	// Now run the encoder's pitch detection
	encoder := NewEncoder(BandwidthWideband)
	pitchLags := encoder.detectPitch(pcm, 4)
	t.Logf("Encoder's detectPitch result: %v", pitchLags)
	t.Logf("Encoder's ltpCorr after detection: %.4f", encoder.pitchState.ltpCorr)

	// Manually trace the Stage 1 computation
	// Downsample 16kHz -> 8kHz -> 4kHz
	dsRatio8k := 2 // 16/8
	frame8kHz := downsampleLowpass(pcm, dsRatio8k)
	frame4kHz := downsampleLowpass(frame8kHz, 2)

	t.Logf("Frame lengths: 16kHz=%d, 8kHz=%d, 4kHz=%d", len(pcm), len(frame8kHz), len(frame4kHz))

	// Apply the same low-pass filter as pitch detection
	for i := len(frame4kHz) - 1; i > 0; i-- {
		frame4kHz[i] = frame4kHz[i] + frame4kHz[i-1]
	}

	// Compute Stage 1 correlation at 4kHz
	// Parameters matching pitch_detect.go
	sfLength4kHz := 5 * 4                  // peSubfrLengthMS=5, 4kHz
	targetStart := sfLength4kHz * 4        // After LTP memory
	minLag4kHz := 2 * 4                    // peMinLagMS=2 at 4kHz = 8
	maxLag4kHz := 18 * 4                   // peMaxLagMS=18 at 4kHz = 72
	expectedLag4kHz := expectedLag / 4     // 53/4 ≈ 13

	t.Logf("4kHz params: sfLength=%d, targetStart=%d, minLag=%d, maxLag=%d, expectedLag=%d",
		sfLength4kHz, targetStart, minLag4kHz, maxLag4kHz, expectedLag4kHz)

	// Compute correlation for expected lag and a few others
	for _, testLag := range []int{expectedLag4kHz, 8, 16, 20} {
		if targetStart+sfLength4kHz > len(frame4kHz) {
			t.Logf("Warning: targetStart+sfLength4kHz > len(frame4kHz)")
			continue
		}
		target := frame4kHz[targetStart : targetStart+sfLength4kHz]

		var targetEnergy float64
		for _, s := range target {
			targetEnergy += float64(s) * float64(s)
		}

		basisIdx := targetStart - testLag
		if basisIdx < 0 || basisIdx+sfLength4kHz > len(frame4kHz) {
			t.Logf("Lag %d: out of bounds (basisIdx=%d)", testLag, basisIdx)
			continue
		}
		basis := frame4kHz[basisIdx : basisIdx+sfLength4kHz]

		var xcorr, basisEnergy float64
		for i := 0; i < sfLength4kHz; i++ {
			xcorr += float64(target[i]) * float64(basis[i])
			basisEnergy += float64(basis[i]) * float64(basis[i])
		}

		normalizer := targetEnergy + basisEnergy + float64(sfLength4kHz)*4000.0
		corr := 2 * xcorr / normalizer

		t.Logf("4kHz Lag %d: xcorr=%.1f, targetE=%.1f, basisE=%.1f, norm=%.1f, C=%.4f",
			testLag, xcorr, targetEnergy, basisEnergy, normalizer, corr)
	}
}

// TestMultiFrameWarmup tests if SILK quality improves after frames warm up.
func TestMultiFrameWarmup(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000 // 320 samples

	// Create encoder and decoder with persistent state
	encoder := NewEncoder(BandwidthWideband)
	decoder := NewDecoder()
	st := &decoder.state[0]
	st.nbSubfr = maxNbSubfr
	silkDecoderSetFs(st, 16) // 16 kHz

	// Encode and decode 5 frames to see warmup effect
	for frame := 0; frame < 5; frame++ {
		// Generate test signal - 300 Hz sine at 16 kHz
		pcm := make([]float32, frameSamples)
		for i := range pcm {
			tm := float64(frame*frameSamples+i) / float64(config.SampleRate)
			pcm[i] = 0.5 * float32(math.Sin(2*math.Pi*300*tm))
		}

		// Trace the encoder's LTP analysis before encoding
		if frame == 0 {
			// Compute expected pitch lag for 300 Hz at 16 kHz
			expectedLag := 16000 / 300 // ~53 samples
			t.Logf("Frame 0: Expected pitch lag for 300Hz = %d samples", expectedLag)
		}

		// Encode
		encoded := encoder.EncodeFrame(pcm, true)

		// After encoding, check the encoder's pitch analysis buffer and LTP correlation
		if frame < 2 {
			var bufSum float64
			for _, s := range encoder.pitchAnalysisBuf {
				bufSum += float64(s) * float64(s)
			}
			bufRMS := math.Sqrt(bufSum / float64(len(encoder.pitchAnalysisBuf)))
			t.Logf("Frame %d: pitchAnalysisBuf len=%d, RMS=%.1f", frame, len(encoder.pitchAnalysisBuf), bufRMS)
		}
		t.Logf("Frame %d: Encoder's ltpCorr = %.4f, prevLag = %d", frame, encoder.ltpCorr, encoder.pitchState.prevLag)

		// Decode
		var rd rangecoding.Decoder
		rd.Init(encoded)

		vadBit := rd.DecodeBit(1)
		_ = rd.DecodeBit(1) // LBRR bit

		silkDecodeIndices(st, &rd, vadBit == 1, codeIndependently)

		var ctrl decoderControl
		silkDecodeParameters(st, &ctrl, codeIndependently)

		pulses := make([]int16, st.frameLength)
		silkDecodePulses(&rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength)

		output := make([]int16, st.frameLength)
		silkDecodeCore(st, &ctrl, output, pulses)

		// CRITICAL: Update outBuf for LTP history (this was missing!)
		// Without this, the decoder's LTP predictions use zeros instead of previous frame data.
		silkUpdateOutBuf(st, output)

		// Compute RMS
		var inSumSq, outSumSq float64
		for i, s := range pcm {
			inSumSq += float64(s*32768) * float64(s*32768)
			outSumSq += float64(output[i]) * float64(output[i])
		}
		inRMS := math.Sqrt(inSumSq / float64(len(pcm)))
		outRMS := math.Sqrt(outSumSq / float64(len(output)))

		t.Logf("Frame %d: Input RMS=%.1f, Output RMS=%.1f, Ratio=%.4f, SignalType=%d, GainQ16=%d",
			frame, inRMS, outRMS, outRMS/inRMS, st.indices.signalType, ctrl.GainsQ16[0])

		// On first frame, trace the LPC coefficients
		if frame == 0 {
			t.Logf("  Decoder LPC[0-7]: %v", ctrl.PredCoefQ12[0][:8])
			t.Logf("  Decoder LPC[8-15]: %v", ctrl.PredCoefQ12[0][8:16])
			t.Logf("  Pitch lags: %v", ctrl.pitchL[:4])
			t.Logf("  LTP coeffs[0]: %v", ctrl.LTPCoefQ14[:5])
			t.Logf("  PERIndex: %d", st.indices.PERIndex)
			t.Logf("  LTPIndex: %v", st.indices.LTPIndex[:4])
		}
	}
}
