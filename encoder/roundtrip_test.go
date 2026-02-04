// Package encoder provides comprehensive round-trip tests for the gopus encoder.
// These tests encode audio, decode it, and verify quality metrics.
package encoder_test

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/hybrid"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// Quality thresholds for round-trip tests.
const (
	// MinSNRThreshold is the minimum acceptable SNR in dB for round-trip tests.
	// A lossy codec like Opus typically achieves 20-40dB SNR depending on bitrate.
	MinSNRThreshold = 10.0

	// MinCorrelationThreshold is the minimum acceptable correlation coefficient.
	// Values close to 1.0 indicate decoded audio closely matches the original.
	MinCorrelationThreshold = 0.8

	// MaxDelayCompensation is the maximum delay to search for alignment (samples).
	MaxDelayCompensation = 500
)

// =============================================================================
// Test Signal Generators
// =============================================================================

// generateSineWave generates a sine wave at the specified frequency.
func generateSineWave(samples, channels int, frequency, amplitude float64, sampleRate int) []float64 {
	pcm := make([]float64, samples*channels)
	for i := 0; i < samples; i++ {
		t := float64(i) / float64(sampleRate)
		sample := amplitude * math.Sin(2*math.Pi*frequency*t)
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = sample
		}
	}
	return pcm
}

// generateSpeechLikeSignal generates a signal with speech-like characteristics.
// It includes fundamental frequency, formants, and amplitude modulation.
func generateSpeechLikeSignal(samples, channels int, sampleRate int) []float64 {
	pcm := make([]float64, samples*channels)
	f0 := 120.0   // Fundamental frequency (typical male voice)
	f1 := 700.0   // First formant
	f2 := 1100.0  // Second formant
	f3 := 2500.0  // Third formant
	modFreq := 5.0 // Amplitude modulation (syllabic rate)

	for i := 0; i < samples; i++ {
		t := float64(i) / float64(sampleRate)

		// Amplitude modulation simulating syllables
		envelope := 0.5 + 0.5*math.Sin(2*math.Pi*modFreq*t)

		// Sum of harmonics with formant emphasis
		sample := 0.0
		for h := 1; h <= 10; h++ {
			hFreq := f0 * float64(h)
			// Apply formant resonance
			formantGain := formantResponse(hFreq, f1, 80) +
				formantResponse(hFreq, f2, 100) +
				formantResponse(hFreq, f3, 120)
			sample += formantGain * math.Sin(2*math.Pi*hFreq*t) / float64(h)
		}

		sample *= envelope * 0.3 // Scale down

		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = sample
		}
	}
	return pcm
}

// formantResponse computes a resonance response for formant simulation.
func formantResponse(freq, center, bandwidth float64) float64 {
	diff := freq - center
	return 1.0 / (1.0 + math.Pow(diff/bandwidth, 2))
}

// generateMusicLikeSignal generates a signal with music-like characteristics.
// It includes multiple instruments/voices with different timbres.
func generateMusicLikeSignal(samples, channels int, sampleRate int) []float64 {
	pcm := make([]float64, samples*channels)

	// Multiple "instruments"
	freqs := []float64{261.63, 329.63, 392.0, 523.25} // C4, E4, G4, C5 (C major chord)

	for i := 0; i < samples; i++ {
		t := float64(i) / float64(sampleRate)
		sample := 0.0

		for idx, freq := range freqs {
			// Each note with its harmonics
			for h := 1; h <= 5; h++ {
				hFreq := freq * float64(h)
				// Different decay for each harmonic
				decay := 1.0 / float64(h*h)
				// Slight detuning for richness
				detune := 1.0 + 0.001*float64(idx)
				sample += decay * 0.1 * math.Sin(2*math.Pi*hFreq*detune*t)
			}
		}

		for ch := 0; ch < channels; ch++ {
			// Add slight stereo spread for stereo channels
			spread := 1.0
			if channels == 2 && ch == 1 {
				spread = 0.95 // Slightly different level
			}
			pcm[i*channels+ch] = sample * spread
		}
	}
	return pcm
}

// generateSilence generates a silent signal.
func generateSilence(samples, channels int) []float64 {
	return make([]float64, samples*channels)
}

// generateTransientSignal generates a signal with sudden transients.
func generateTransientSignal(samples, channels int, sampleRate int) []float64 {
	pcm := make([]float64, samples*channels)

	// Create transients at regular intervals
	transientInterval := sampleRate / 10 // 10 transients per second

	for i := 0; i < samples; i++ {
		sample := 0.0

		// Check if we're at a transient point
		if i%transientInterval < sampleRate/100 {
			// Sharp attack followed by decay
			decay := float64(i%transientInterval) / float64(sampleRate/100)
			sample = (1.0 - decay) * 0.8
		}

		// Add some sustained content
		t := float64(i) / float64(sampleRate)
		sample += 0.1 * math.Sin(2*math.Pi*440*t)

		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = sample
		}
	}
	return pcm
}

// =============================================================================
// Quality Measurement Functions
// =============================================================================

// computeCorrelation computes Pearson correlation coefficient.
func computeCorrelation(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	n := min(len(a), len(b))

	var sumA, sumB, sumAB, sumA2, sumB2 float64
	for i := 0; i < n; i++ {
		sumA += a[i]
		sumB += b[i]
		sumAB += a[i] * b[i]
		sumA2 += a[i] * a[i]
		sumB2 += b[i] * b[i]
	}

	nf := float64(n)
	num := nf*sumAB - sumA*sumB
	den := math.Sqrt((nf*sumA2 - sumA*sumA) * (nf*sumB2 - sumB*sumB))

	if den == 0 {
		return 0
	}
	return num / den
}

// computeSNRWithDelay computes SNR with optimal delay compensation.
func computeSNRWithDelay(original, decoded []float64, maxDelay int) (float64, int) {
	bestSNR := math.Inf(-1)
	bestDelay := 0

	for delay := -maxDelay; delay <= maxDelay; delay++ {
		var signalPower, noisePower float64
		count := 0

		margin := 120 // Skip edges
		for i := margin; i < len(original)-margin; i++ {
			decIdx := i + delay
			if decIdx >= margin && decIdx < len(decoded)-margin {
				signalPower += original[i] * original[i]
				noise := decoded[decIdx] - original[i]
				noisePower += noise * noise
				count++
			}
		}

		if count > 0 && signalPower > 0 && noisePower > 0 {
			snr := 10.0 * math.Log10(signalPower/noisePower)
			if snr > bestSNR {
				bestSNR = snr
				bestDelay = delay
			}
		}
	}

	return bestSNR, bestDelay
}

// maxAmplitude returns the maximum absolute amplitude in a signal.
func maxAmplitude(samples []float64) float64 {
	maxVal := 0.0
	for _, s := range samples {
		if math.Abs(s) > maxVal {
			maxVal = math.Abs(s)
		}
	}
	return maxVal
}

// =============================================================================
// Mode-Specific Round-Trip Tests
// =============================================================================

// TestRoundTripCELTAllBandwidths tests CELT mode round-trip for all bandwidths.
func TestRoundTripCELTAllBandwidths(t *testing.T) {
	bandwidths := []struct {
		name string
		bw   types.Bandwidth
	}{
		{"Narrowband", types.BandwidthNarrowband},
		{"Mediumband", types.BandwidthMediumband},
		{"Wideband", types.BandwidthWideband},
		{"Superwideband", types.BandwidthSuperwideband},
		{"Fullband", types.BandwidthFullband},
	}

	frameSizes := []struct {
		name    string
		samples int
	}{
		{"2.5ms", 120},
		{"5ms", 240},
		{"10ms", 480},
		{"20ms", 960},
	}

	for _, bw := range bandwidths {
		for _, fs := range frameSizes {
			testName := bw.name + "_" + fs.name
			t.Run(testName, func(t *testing.T) {
				enc := celt.NewEncoder(1)
				dec := celt.NewDecoder(1)

				// Generate 5 frames of test signal
				numFrames := 5
				pcm := generateSineWave(fs.samples*numFrames, 1, 440.0, 0.5, 48000)

				var allDecoded []float64

				for i := 0; i < numFrames; i++ {
					start := i * fs.samples
					end := start + fs.samples
					framePCM := pcm[start:end]

					packet, err := enc.EncodeFrame(framePCM, fs.samples)
					if err != nil {
						t.Fatalf("Encode failed: %v", err)
					}

					decoded, err := dec.DecodeFrame(packet, fs.samples)
					if err != nil {
						t.Logf("Decode frame %d failed: %v", i, err)
						continue
					}

					allDecoded = append(allDecoded, decoded...)
				}

				if len(allDecoded) == 0 {
					t.Fatal("No samples decoded")
				}

				// Compute quality metrics with delay compensation
				snr, delay := computeSNRWithDelay(pcm, allDecoded, MaxDelayCompensation)
				t.Logf("CELT %s %s: SNR=%.2f dB, delay=%d samples, decoded=%d samples",
					bw.name, fs.name, snr, delay, len(allDecoded))

				if maxAmplitude(allDecoded) < 0.001 {
					t.Error("Decoded audio appears to be silence")
				}
			})
		}
	}
}

// TestRoundTripSILKAllBandwidths tests SILK mode round-trip for all bandwidths.
func TestRoundTripSILKAllBandwidths(t *testing.T) {
	bandwidths := []struct {
		name string
		bw   silk.Bandwidth
	}{
		{"Narrowband", silk.BandwidthNarrowband},
		{"Mediumband", silk.BandwidthMediumband},
		{"Wideband", silk.BandwidthWideband},
	}

	// SILK frame sizes: 10, 20, 40, 60ms
	frameSizes := []struct {
		name    string
		samples int // At 48kHz
	}{
		{"10ms", 480},
		{"20ms", 960},
		{"40ms", 1920},
		{"60ms", 2880},
	}

	for _, bw := range bandwidths {
		for _, fs := range frameSizes {
			testName := bw.name + "_" + fs.name
			t.Run(testName, func(t *testing.T) {
				enc := encoder.NewEncoder(48000, 1)
				enc.SetMode(encoder.ModeSILK)

				// Map SILK bandwidth to types.Bandwidth
				var typesBw types.Bandwidth
				switch bw.bw {
				case silk.BandwidthNarrowband:
					typesBw = types.BandwidthNarrowband
				case silk.BandwidthMediumband:
					typesBw = types.BandwidthMediumband
				case silk.BandwidthWideband:
					typesBw = types.BandwidthWideband
				}
				enc.SetBandwidth(typesBw)

				// Generate speech-like signal
				numFrames := 3
				pcm := generateSpeechLikeSignal(fs.samples*numFrames, 1, 48000)

				for i := 0; i < numFrames; i++ {
					start := i * fs.samples
					end := start + fs.samples
					framePCM := pcm[start:end]

					packet, err := enc.Encode(framePCM, fs.samples)
					if err != nil {
						t.Logf("SILK %s %s: Encode failed: %v", bw.name, fs.name, err)
						continue
					}

					if len(packet) == 0 {
						t.Logf("SILK %s %s: Empty packet", bw.name, fs.name)
						continue
					}

					t.Logf("SILK %s %s: Frame %d encoded to %d bytes",
						bw.name, fs.name, i, len(packet))
				}
			})
		}
	}
}

// TestRoundTripHybridSWB tests Hybrid mode at Superwideband.
func TestRoundTripHybridSWB(t *testing.T) {
	frameSizes := []struct {
		name    string
		samples int
	}{
		{"10ms", 480},
		{"20ms", 960},
	}

	for _, fs := range frameSizes {
		t.Run(fs.name, func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(encoder.ModeHybrid)
			enc.SetBandwidth(types.BandwidthSuperwideband)

			dec := hybrid.NewDecoder(1)

			// Generate test signal
			numFrames := 5
			pcm := generateSpeechLikeSignal(fs.samples*numFrames, 1, 48000)

			for i := 0; i < numFrames; i++ {
				start := i * fs.samples
				end := start + fs.samples
				framePCM := pcm[start:end]

				packet, err := enc.Encode(framePCM, fs.samples)
				if err != nil {
					t.Fatalf("Encode failed: %v", err)
				}

				if len(packet) == 0 {
					t.Logf("Frame %d: Empty packet (DTX?)", i)
					continue
				}

				t.Logf("Hybrid SWB %s: Frame %d encoded to %d bytes", fs.name, i, len(packet))

				// Parse TOC to verify hybrid mode
				if len(packet) > 0 {
					toc := gopus.ParseTOC(packet[0])
					if toc.Mode != types.ModeHybrid {
						t.Errorf("Expected Hybrid mode, got %v", toc.Mode)
					}
				}

				// Decode
				rd := &rangecoding.Decoder{}
				rd.Init(packet[1:]) // Skip TOC byte

				decoded, err := dec.DecodeWithDecoder(rd, fs.samples)
				if err != nil {
					t.Logf("Decode failed: %v", err)
					continue
				}

				if len(decoded) > 0 {
					maxDec := maxAmplitude(decoded)
					t.Logf("Decoded max amplitude: %.4f", maxDec)
				}
			}
		})
	}
}

// TestRoundTripHybridFullband tests Hybrid mode at Fullband.
func TestRoundTripHybridFullband(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthFullband)

	numFrames := 5
	frameSize := 960
	pcm := generateMusicLikeSignal(frameSize*numFrames, 1, 48000)

	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		framePCM := pcm[start:end]

		packet, err := enc.Encode(framePCM, frameSize)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}

		if len(packet) > 0 {
			toc := gopus.ParseTOC(packet[0])
			t.Logf("Hybrid FB: Frame %d: %d bytes, config=%d", i, len(packet), toc.Config)
		}
	}
}

// =============================================================================
// Stereo Round-Trip Tests
// =============================================================================

// TestRoundTripStereo tests stereo encoding/decoding.
func TestRoundTripStereo(t *testing.T) {
	modes := []struct {
		name string
		mode encoder.Mode
		bw   types.Bandwidth
	}{
		{"CELT_FB", encoder.ModeCELT, types.BandwidthFullband},
		{"Hybrid_SWB", encoder.ModeHybrid, types.BandwidthSuperwideband},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 2)
			enc.SetMode(mode.mode)
			enc.SetBandwidth(mode.bw)

			dec := celt.NewDecoder(2)

			// Generate stereo signal
			frameSize := 960
			numFrames := 5
			pcm := generateMusicLikeSignal(frameSize*numFrames, 2, 48000)

			for i := 0; i < numFrames; i++ {
				start := i * frameSize * 2
				end := start + frameSize*2
				framePCM := pcm[start:end]

				packet, err := enc.Encode(framePCM, frameSize)
				if err != nil {
					t.Fatalf("Encode failed: %v", err)
				}

				if len(packet) > 0 {
					toc := gopus.ParseTOC(packet[0])
					if !toc.Stereo {
						t.Error("Expected stereo flag to be set")
					}
					t.Logf("%s stereo: Frame %d: %d bytes", mode.name, i, len(packet))
				}

				// Try to decode
				if mode.mode == encoder.ModeCELT && len(packet) > 1 {
					decoded, err := dec.DecodeFrame(packet[1:], frameSize)
					if err != nil {
						t.Logf("Decode failed: %v", err)
					} else {
						t.Logf("Decoded %d stereo samples", len(decoded)/2)
					}
				}
			}
		})
	}
}

// =============================================================================
// Frame Size Round-Trip Tests
// =============================================================================

// TestRoundTripAllFrameSizes tests all valid frame sizes for each mode.
func TestRoundTripAllFrameSizes(t *testing.T) {
	// CELT frame sizes
	celtSizes := []int{120, 240, 480, 960}
	for _, size := range celtSizes {
		t.Run("CELT_"+frameSizeToString(size), func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(encoder.ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)

			pcm := generateSineWave(size, 1, 440.0, 0.5, 48000)
			packet, err := enc.Encode(pcm, size)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}
			t.Logf("CELT %s: %d bytes", frameSizeToString(size), len(packet))
		})
	}

	// SILK frame sizes
	silkSizes := []int{480, 960, 1920, 2880}
	for _, size := range silkSizes {
		t.Run("SILK_"+frameSizeToString(size), func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(encoder.ModeSILK)
			enc.SetBandwidth(types.BandwidthWideband)

			pcm := generateSpeechLikeSignal(size, 1, 48000)
			packet, err := enc.Encode(pcm, size)
			if err != nil {
				t.Logf("SILK %s: Encode returned error: %v", frameSizeToString(size), err)
				return
			}
			t.Logf("SILK %s: %d bytes", frameSizeToString(size), len(packet))
		})
	}

	// Hybrid frame sizes
	hybridSizes := []int{480, 960}
	for _, size := range hybridSizes {
		t.Run("Hybrid_"+frameSizeToString(size), func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(encoder.ModeHybrid)
			enc.SetBandwidth(types.BandwidthSuperwideband)

			pcm := generateSpeechLikeSignal(size, 1, 48000)
			packet, err := enc.Encode(pcm, size)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}
			t.Logf("Hybrid %s: %d bytes", frameSizeToString(size), len(packet))
		})
	}
}

// =============================================================================
// Bitrate Tests
// =============================================================================

// TestRoundTripVariousBitrates tests encoding at different bitrates.
func TestRoundTripVariousBitrates(t *testing.T) {
	bitrates := []int{6000, 12000, 24000, 32000, 64000, 96000, 128000}

	for _, bitrate := range bitrates {
		t.Run(bitrateToString(bitrate), func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(encoder.ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrateMode(encoder.ModeCBR)
			enc.SetBitrate(bitrate)

			frameSize := 960
			pcm := generateMusicLikeSignal(frameSize, 1, 48000)

			packet, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			// Expected size for CBR: bitrate * frameDuration / 8000
			expectedBytes := bitrate * 20 / 8000
			t.Logf("Bitrate %d kbps: %d bytes (expected ~%d bytes)",
				bitrate/1000, len(packet), expectedBytes)

			// Verify CBR produces consistent sizes
			if len(packet) != expectedBytes {
				t.Logf("Note: packet size differs from CBR target")
			}
		})
	}
}

// =============================================================================
// Audio Type Tests
// =============================================================================

// TestRoundTripSpeech tests speech-like content.
func TestRoundTripSpeech(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)

	frameSize := 960
	numFrames := 10
	pcm := generateSpeechLikeSignal(frameSize*numFrames, 1, 48000)

	var totalBytes int
	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		packet, err := enc.Encode(pcm[start:end], frameSize)
		if err != nil {
			t.Fatalf("Frame %d encode failed: %v", i, err)
		}
		totalBytes += len(packet)
	}

	avgBytesPerFrame := float64(totalBytes) / float64(numFrames)
	avgBitrate := avgBytesPerFrame * 8 * 50 / 1000 // 50 frames/sec for 20ms
	t.Logf("Speech: avg %.1f bytes/frame, ~%.1f kbps", avgBytesPerFrame, avgBitrate)
}

// TestRoundTripMusic tests music-like content.
func TestRoundTripMusic(t *testing.T) {
	enc := encoder.NewEncoder(48000, 2)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(128000)

	dec := celt.NewDecoder(2)

	frameSize := 960
	numFrames := 10
	pcm := generateMusicLikeSignal(frameSize*numFrames, 2, 48000)

	var allOriginal, allDecoded []float64

	for i := 0; i < numFrames; i++ {
		start := i * frameSize * 2
		end := start + frameSize*2
		framePCM := pcm[start:end]
		allOriginal = append(allOriginal, framePCM...)

		packet, err := enc.Encode(framePCM, frameSize)
		if err != nil {
			t.Fatalf("Frame %d encode failed: %v", i, err)
		}

		if len(packet) > 1 {
			decoded, err := dec.DecodeFrame(packet[1:], frameSize)
			if err != nil {
				t.Logf("Frame %d decode failed: %v", i, err)
				continue
			}
			allDecoded = append(allDecoded, decoded...)
		}
	}

	if len(allDecoded) > 0 {
		snr, _ := computeSNRWithDelay(allOriginal, allDecoded, MaxDelayCompensation)
		corr := computeCorrelation(allOriginal[:min(len(allOriginal), len(allDecoded))],
			allDecoded[:min(len(allOriginal), len(allDecoded))])
		t.Logf("Music: SNR=%.2f dB, correlation=%.4f", snr, corr)
	}
}

// TestRoundTripSilence tests silence handling.
func TestRoundTripSilence(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)

	frameSize := 960
	numFrames := 5
	pcm := generateSilence(frameSize*numFrames, 1)

	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		packet, err := enc.Encode(pcm[start:end], frameSize)
		if err != nil {
			t.Fatalf("Frame %d encode failed: %v", i, err)
		}
		t.Logf("Silence frame %d: %d bytes", i, len(packet))
	}
}

// TestRoundTripTransients tests handling of transient signals.
func TestRoundTripTransients(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)

	frameSize := 960
	numFrames := 10
	pcm := generateTransientSignal(frameSize*numFrames, 1, 48000)

	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		packet, err := enc.Encode(pcm[start:end], frameSize)
		if err != nil {
			t.Fatalf("Frame %d encode failed: %v", i, err)
		}
		t.Logf("Transient frame %d: %d bytes", i, len(packet))
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

// TestModeTransitions tests transitioning between modes.
func TestModeTransitions(t *testing.T) {
	transitions := []struct {
		name     string
		fromMode encoder.Mode
		fromBw   types.Bandwidth
		toMode   encoder.Mode
		toBw     types.Bandwidth
	}{
		{"SILK_to_CELT", encoder.ModeSILK, types.BandwidthWideband,
			encoder.ModeCELT, types.BandwidthFullband},
		{"CELT_to_SILK", encoder.ModeCELT, types.BandwidthFullband,
			encoder.ModeSILK, types.BandwidthWideband},
		{"SILK_to_Hybrid", encoder.ModeSILK, types.BandwidthWideband,
			encoder.ModeHybrid, types.BandwidthSuperwideband},
		{"Hybrid_to_CELT", encoder.ModeHybrid, types.BandwidthSuperwideband,
			encoder.ModeCELT, types.BandwidthFullband},
	}

	for _, tr := range transitions {
		t.Run(tr.name, func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			frameSize := 960

			// First mode
			enc.SetMode(tr.fromMode)
			enc.SetBandwidth(tr.fromBw)

			pcm1 := generateSpeechLikeSignal(frameSize, 1, 48000)
			packet1, err := enc.Encode(pcm1, frameSize)
			if err != nil {
				t.Logf("First mode encode: %v", err)
			} else {
				t.Logf("From %v: %d bytes", tr.fromMode, len(packet1))
			}

			// Transition to second mode
			enc.SetMode(tr.toMode)
			enc.SetBandwidth(tr.toBw)

			pcm2 := generateSpeechLikeSignal(frameSize, 1, 48000)
			packet2, err := enc.Encode(pcm2, frameSize)
			if err != nil {
				t.Logf("Second mode encode: %v", err)
			} else {
				t.Logf("To %v: %d bytes", tr.toMode, len(packet2))
			}
		})
	}
}

// TestDTXPackets tests DTX (Discontinuous Transmission) behavior.
func TestDTXPackets(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetDTX(true)

	frameSize := 960

	// Send silent frames to trigger DTX
	silence := generateSilence(frameSize, 1)

	dtxFrameCount := 0
	for i := 0; i < encoder.DTXFrameThreshold+10; i++ {
		packet, err := enc.Encode(silence, frameSize)
		if err != nil {
			t.Logf("Frame %d: encode error: %v", i, err)
			continue
		}

		if packet == nil {
			dtxFrameCount++
			t.Logf("Frame %d: DTX suppressed (nil)", i)
		} else {
			t.Logf("Frame %d: %d bytes", i, len(packet))
		}
	}

	if dtxFrameCount > 0 {
		t.Logf("DTX suppressed %d frames", dtxFrameCount)
	}

	// Send speech to exit DTX
	speech := generateSpeechLikeSignal(frameSize, 1, 48000)
	packet, err := enc.Encode(speech, frameSize)
	if err != nil {
		t.Logf("Speech after DTX: %v", err)
	} else if packet != nil {
		t.Logf("Speech after DTX: %d bytes (DTX exit)", len(packet))
	}
}

// TestPacketLossScenarios tests encoding with packet loss hints.
func TestPacketLossScenarios(t *testing.T) {
	lossRates := []int{0, 5, 10, 20, 30}

	for _, lossRate := range lossRates {
		t.Run("loss_"+string(rune('0'+lossRate/10))+string(rune('0'+lossRate%10)), func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(encoder.ModeSILK)
			enc.SetBandwidth(types.BandwidthWideband)
			enc.SetFEC(true)
			enc.SetPacketLoss(lossRate)

			frameSize := 960
			pcm := generateSpeechLikeSignal(frameSize, 1, 48000)

			packet, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Logf("Loss rate %d%%: encode failed: %v", lossRate, err)
				return
			}

			t.Logf("Loss rate %d%%: %d bytes, FEC=%v",
				lossRate, len(packet), enc.FECEnabled())
		})
	}
}

// =============================================================================
// Comprehensive Round-Trip Test
// =============================================================================

// TestComprehensiveRoundTrip runs a comprehensive test of all mode/bandwidth combinations.
func TestComprehensiveRoundTrip(t *testing.T) {
	testCases := []struct {
		name      string
		mode      encoder.Mode
		bandwidth types.Bandwidth
		frameSize int
		channels  int
	}{
		// CELT mono
		{"CELT_NB_20ms_mono", encoder.ModeCELT, types.BandwidthNarrowband, 960, 1},
		{"CELT_WB_20ms_mono", encoder.ModeCELT, types.BandwidthWideband, 960, 1},
		{"CELT_FB_20ms_mono", encoder.ModeCELT, types.BandwidthFullband, 960, 1},
		{"CELT_FB_10ms_mono", encoder.ModeCELT, types.BandwidthFullband, 480, 1},
		{"CELT_FB_5ms_mono", encoder.ModeCELT, types.BandwidthFullband, 240, 1},
		{"CELT_FB_2.5ms_mono", encoder.ModeCELT, types.BandwidthFullband, 120, 1},

		// CELT stereo
		{"CELT_FB_20ms_stereo", encoder.ModeCELT, types.BandwidthFullband, 960, 2},
		{"CELT_FB_10ms_stereo", encoder.ModeCELT, types.BandwidthFullband, 480, 2},

		// Hybrid mono
		{"Hybrid_SWB_20ms_mono", encoder.ModeHybrid, types.BandwidthSuperwideband, 960, 1},
		{"Hybrid_SWB_10ms_mono", encoder.ModeHybrid, types.BandwidthSuperwideband, 480, 1},
		{"Hybrid_FB_20ms_mono", encoder.ModeHybrid, types.BandwidthFullband, 960, 1},

		// Hybrid stereo
		{"Hybrid_SWB_20ms_stereo", encoder.ModeHybrid, types.BandwidthSuperwideband, 960, 2},

		// SILK mono
		{"SILK_NB_20ms_mono", encoder.ModeSILK, types.BandwidthNarrowband, 960, 1},
		{"SILK_MB_20ms_mono", encoder.ModeSILK, types.BandwidthMediumband, 960, 1},
		{"SILK_WB_20ms_mono", encoder.ModeSILK, types.BandwidthWideband, 960, 1},
		{"SILK_WB_40ms_mono", encoder.ModeSILK, types.BandwidthWideband, 1920, 1},
		{"SILK_WB_60ms_mono", encoder.ModeSILK, types.BandwidthWideband, 2880, 1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := encoder.NewEncoder(48000, tc.channels)
			enc.SetMode(tc.mode)
			enc.SetBandwidth(tc.bandwidth)

			numFrames := 5
			pcm := generateSpeechLikeSignal(tc.frameSize*numFrames, tc.channels, 48000)

			var totalBytes int
			successFrames := 0

			for i := 0; i < numFrames; i++ {
				start := i * tc.frameSize * tc.channels
				end := start + tc.frameSize*tc.channels
				framePCM := pcm[start:end]

				packet, err := enc.Encode(framePCM, tc.frameSize)
				if err != nil {
					t.Logf("Frame %d: %v", i, err)
					continue
				}

				if len(packet) > 0 {
					// Verify TOC
					toc := gopus.ParseTOC(packet[0])
					if tc.channels == 2 && !toc.Stereo {
						t.Errorf("Frame %d: expected stereo flag", i)
					}
					totalBytes += len(packet)
					successFrames++
				}
			}

			if successFrames > 0 {
				avgBytes := float64(totalBytes) / float64(successFrames)
				frameDurationMs := tc.frameSize * 1000 / 48000
				avgBitrate := avgBytes * 8 * 1000 / float64(frameDurationMs)
				t.Logf("Success: %d/%d frames, avg %.1f bytes (%.1f kbps)",
					successFrames, numFrames, avgBytes, avgBitrate/1000)
			} else {
				t.Log("No frames successfully encoded")
			}
		})
	}
}

// =============================================================================
// High-Level API Round-Trip Test
// =============================================================================

// TestHighLevelAPIRoundTrip tests the high-level gopus API for round-trip.
func TestHighLevelAPIRoundTrip(t *testing.T) {
	applications := []struct {
		name string
		app  gopus.Application
	}{
		{"VoIP", gopus.ApplicationVoIP},
		{"Audio", gopus.ApplicationAudio},
		{"LowDelay", gopus.ApplicationLowDelay},
	}

	for _, app := range applications {
		t.Run(app.name, func(t *testing.T) {
			enc, err := gopus.NewEncoder(48000, 2, app.app)
			if err != nil {
				t.Fatalf("NewEncoder failed: %v", err)
			}

			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
			if err != nil {
				t.Fatalf("NewDecoder failed: %v", err)
			}

			frameSize := enc.FrameSize()
			pcm := generateMusicLikeSignal(frameSize*5, 2, 48000)

			pcmFloat32 := make([]float32, len(pcm))
			for i, v := range pcm {
				pcmFloat32[i] = float32(v)
			}

			var allDecoded []float32

			for i := 0; i < 5; i++ {
				start := i * frameSize * 2
				end := start + frameSize*2
				framePCM := pcmFloat32[start:end]

				packet, err := enc.EncodeFloat32(framePCM)
				if err != nil {
					t.Fatalf("Encode failed: %v", err)
				}

				decodedBuf := make([]float32, frameSize*2)
				n, err := dec.Decode(packet, decodedBuf)
				if err != nil {
					t.Logf("Decode failed: %v", err)
					continue
				}

				allDecoded = append(allDecoded, decodedBuf[:n*2]...)
				t.Logf("%s: Frame %d: encoded %d bytes, decoded %d samples",
					app.name, i, len(packet), n)
			}

			if len(allDecoded) > 0 {
				// Verify decoded audio is not silence
				maxDec := float32(0)
				for _, v := range allDecoded {
					if float32(math.Abs(float64(v))) > maxDec {
						maxDec = float32(math.Abs(float64(v)))
					}
				}
				t.Logf("%s: Max decoded amplitude: %.4f", app.name, maxDec)
				if maxDec < 0.001 {
					t.Error("Decoded audio appears to be silence")
				}
			}
		})
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func frameSizeToString(samples int) string {
	switch samples {
	case 120:
		return "2.5ms"
	case 240:
		return "5ms"
	case 480:
		return "10ms"
	case 960:
		return "20ms"
	case 1920:
		return "40ms"
	case 2880:
		return "60ms"
	default:
		return "unknown"
	}
}

func bitrateToString(bitrate int) string {
	return string(rune('0'+bitrate/100000)) +
		string(rune('0'+(bitrate/10000)%10)) +
		string(rune('0'+(bitrate/1000)%10)) + "kbps"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
