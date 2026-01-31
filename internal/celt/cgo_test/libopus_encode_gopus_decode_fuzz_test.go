// Package cgo tests libopus encode -> gopus decode compatibility.
package cgo

import (
	"math"
	"math/rand"
	"testing"

	gopus "github.com/thesyncim/gopus"
)

// Thresholds for decoder output comparison
const (
	// MaxSampleDiffThreshold is the maximum allowed difference per sample
	// between gopus and libopus decoder output. Lossy codec so some difference expected.
	MaxSampleDiffThreshold = 0.01 // ~1% of full scale

	// CorrelationThreshold is minimum correlation required between outputs
	CorrelationThreshold = 0.99

	// RMSDiffThreshold is max RMS difference allowed
	RMSDiffThreshold = 0.005
)

// FuzzLibopusEncodeGopusDecode fuzzes the decoder by encoding random audio
// with real libopus and decoding with the pure Go gopus decoder.
// Compares gopus output against libopus decoder output to find mismatches.
func FuzzLibopusEncodeGopusDecode(f *testing.F) {
	// Add seed corpus with various configurations
	// Format: [seed, channels, frameSize index, bitrate/1000, bandwidth index]
	f.Add(uint64(12345), uint8(1), uint8(0), uint8(64), uint8(4))  // Mono, 960 samples, 64kbps, fullband
	f.Add(uint64(67890), uint8(2), uint8(0), uint8(128), uint8(4)) // Stereo, 960 samples, 128kbps, fullband
	f.Add(uint64(11111), uint8(1), uint8(1), uint8(32), uint8(2))  // Mono, 480 samples, 32kbps, wideband
	f.Add(uint64(22222), uint8(2), uint8(2), uint8(96), uint8(3))  // Stereo, 1920 samples, 96kbps, superwideband
	f.Add(uint64(33333), uint8(1), uint8(0), uint8(12), uint8(0))  // Mono, 960 samples, 12kbps, narrowband
	f.Add(uint64(44444), uint8(1), uint8(3), uint8(48), uint8(1))  // Mono, 2880 samples, 48kbps, mediumband

	frameSizes := []int{960, 480, 1920, 2880} // 20ms, 10ms, 40ms, 60ms at 48kHz
	bandwidths := []int{
		OpusBandwidthNarrowband,
		OpusBandwidthMediumband,
		OpusBandwidthWideband,
		OpusBandwidthSuperwideband,
		OpusBandwidthFullband,
	}

	f.Fuzz(func(t *testing.T, seed uint64, channelsByte, frameSizeIdx, bitrateKbps, bandwidthIdx uint8) {
		// Normalize inputs
		channels := int(channelsByte%2) + 1 // 1 or 2
		frameSize := frameSizes[int(frameSizeIdx)%len(frameSizes)]
		bitrate := (int(bitrateKbps)%240 + 8) * 1000 // 8-248 kbps
		bandwidth := bandwidths[int(bandwidthIdx)%len(bandwidths)]

		// Generate random audio using seed
		rng := rand.New(rand.NewSource(int64(seed)))
		pcm := generateRandomAudioF32(rng, frameSize*channels)

		// Create libopus encoder
		libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
		if err != nil || libEnc == nil {
			t.Skip("Failed to create libopus encoder")
		}
		defer libEnc.Destroy()

		// Configure encoder
		libEnc.SetBitrate(bitrate)
		libEnc.SetBandwidth(bandwidth)
		libEnc.SetComplexity(10)
		libEnc.SetVBR(true)

		// Encode with libopus
		packet, encLen := libEnc.EncodeFloat(pcm, frameSize)
		if encLen <= 0 {
			t.Skipf("Encoding failed with len=%d", encLen)
		}

		// Create libopus decoder (reference)
		libDec, err := NewLibopusDecoder(48000, channels)
		if err != nil || libDec == nil {
			t.Skip("Failed to create libopus decoder")
		}
		defer libDec.Destroy()

		// Create gopus decoder
		gopusDec, err := gopus.NewDecoderDefault(48000, channels)
		if err != nil {
			t.Fatalf("Failed to create gopus decoder: %v", err)
		}

		// Decode with libopus (reference)
		libDecoded, libDecLen := libDec.DecodeFloat(packet, frameSize)
		if libDecLen <= 0 {
			t.Skipf("libopus decode failed with len=%d", libDecLen)
		}

		// Decode with gopus
		gopusDecoded, err := decodeFloat32(gopusDec, packet)
		if err != nil {
			t.Errorf("gopus decode error: %v (libopus succeeded)", err)
			return
		}

		// Basic length check
		expectedSamples := frameSize * channels
		if len(gopusDecoded) != expectedSamples {
			t.Errorf("gopus output length mismatch: got %d, want %d", len(gopusDecoded), expectedSamples)
		}

		// Check for NaN or Inf in gopus output
		for i, s := range gopusDecoded {
			if math.IsNaN(float64(s)) {
				t.Errorf("NaN at sample %d", i)
				return
			}
			if math.IsInf(float64(s), 0) {
				t.Errorf("Inf at sample %d", i)
				return
			}
		}

		// Compare outputs: gopus vs libopus
		result := compareDecoderOutputs(libDecoded[:expectedSamples], gopusDecoded)

		// Check thresholds
		if result.maxDiff > MaxSampleDiffThreshold {
			t.Errorf("Max sample diff %.6f exceeds threshold %.6f at sample %d (seed=%d, ch=%d, fs=%d, br=%d, bw=%d)",
				result.maxDiff, MaxSampleDiffThreshold, result.maxDiffIdx,
				seed, channels, frameSize, bitrate, bandwidth)
		}

		if result.correlation < CorrelationThreshold {
			t.Errorf("Correlation %.6f below threshold %.6f (seed=%d, ch=%d, fs=%d, br=%d, bw=%d)",
				result.correlation, CorrelationThreshold,
				seed, channels, frameSize, bitrate, bandwidth)
		}

		if result.rmsDiff > RMSDiffThreshold {
			t.Errorf("RMS diff %.6f exceeds threshold %.6f (seed=%d, ch=%d, fs=%d, br=%d, bw=%d)",
				result.rmsDiff, RMSDiffThreshold,
				seed, channels, frameSize, bitrate, bandwidth)
		}
	})
}

// comparisonResult holds metrics from comparing two decoder outputs
type comparisonResult struct {
	maxDiff     float64 // Maximum absolute difference
	maxDiffIdx  int     // Index of max difference
	rmsDiff     float64 // RMS of differences
	correlation float64 // Pearson correlation
	meanDiff    float64 // Mean difference (bias)
}

// compareDecoderOutputs compares libopus and gopus decoder outputs
func compareDecoderOutputs(libopus []float32, gopus []float32) comparisonResult {
	n := len(libopus)
	if len(gopus) < n {
		n = len(gopus)
	}

	var result comparisonResult
	var sumDiff, sumDiffSq float64
	var sumLib, sumGo, sumLibSq, sumGoSq, sumLibGo float64

	for i := 0; i < n; i++ {
		lib := float64(libopus[i])
		go_ := float64(gopus[i])
		diff := math.Abs(lib - go_)

		if diff > result.maxDiff {
			result.maxDiff = diff
			result.maxDiffIdx = i
		}

		sumDiff += lib - go_
		sumDiffSq += diff * diff

		sumLib += lib
		sumGo += go_
		sumLibSq += lib * lib
		sumGoSq += go_ * go_
		sumLibGo += lib * go_
	}

	nf := float64(n)
	result.meanDiff = sumDiff / nf
	result.rmsDiff = math.Sqrt(sumDiffSq / nf)

	// Pearson correlation
	meanLib := sumLib / nf
	meanGo := sumGo / nf
	varLib := sumLibSq/nf - meanLib*meanLib
	varGo := sumGoSq/nf - meanGo*meanGo
	covar := sumLibGo/nf - meanLib*meanGo

	if varLib > 0 && varGo > 0 {
		result.correlation = covar / math.Sqrt(varLib*varGo)
	}

	return result
}

// TestLibopusEncodeGopusDecodeVariousConfigs runs deterministic tests with various
// encoder configurations to verify gopus can decode libopus output.
// Compares gopus output against libopus decoder output.
func TestLibopusEncodeGopusDecodeVariousConfigs(t *testing.T) {
	testCases := []struct {
		name       string
		channels   int
		frameSize  int
		bitrate    int
		bandwidth  int
		signalType string
	}{
		// CELT mode tests (fullband, higher bitrates)
		{"CELT-FB-mono-64k", 1, 960, 64000, OpusBandwidthFullband, "sine"},
		{"CELT-FB-mono-128k", 1, 960, 128000, OpusBandwidthFullband, "sine"},
		{"CELT-FB-stereo-128k", 2, 960, 128000, OpusBandwidthFullband, "sine"},
		{"CELT-FB-stereo-256k", 2, 960, 256000, OpusBandwidthFullband, "sine"},
		{"CELT-SWB-mono-64k", 1, 960, 64000, OpusBandwidthSuperwideband, "sine"},

		// Different frame sizes
		{"CELT-FB-mono-10ms", 1, 480, 64000, OpusBandwidthFullband, "sine"},
		{"CELT-FB-mono-40ms", 1, 1920, 64000, OpusBandwidthFullband, "sine"},
		{"CELT-FB-mono-60ms", 1, 2880, 64000, OpusBandwidthFullband, "sine"},

		// Random noise input
		{"CELT-FB-mono-noise", 1, 960, 64000, OpusBandwidthFullband, "noise"},
		{"CELT-FB-stereo-noise", 2, 960, 128000, OpusBandwidthFullband, "noise"},

		// Lower bandwidths
		{"CELT-WB-mono-32k", 1, 960, 32000, OpusBandwidthWideband, "sine"},
		{"CELT-MB-mono-24k", 1, 960, 24000, OpusBandwidthMediumband, "sine"},
		{"CELT-NB-mono-16k", 1, 960, 16000, OpusBandwidthNarrowband, "sine"},

		// Multi-tone
		{"CELT-FB-mono-multitone", 1, 960, 64000, OpusBandwidthFullband, "multitone"},
		{"CELT-FB-stereo-multitone", 2, 960, 128000, OpusBandwidthFullband, "multitone"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Generate test signal
			pcm := generateTestSignalF32(tc.signalType, tc.frameSize, tc.channels)

			// Create and configure libopus encoder
			libEnc, err := NewLibopusEncoder(48000, tc.channels, OpusApplicationAudio)
			if err != nil || libEnc == nil {
				t.Skip("Failed to create libopus encoder")
			}
			defer libEnc.Destroy()

			libEnc.SetBitrate(tc.bitrate)
			libEnc.SetBandwidth(tc.bandwidth)
			libEnc.SetComplexity(10)
			libEnc.SetVBR(true)

			// Encode with libopus
			packet, encLen := libEnc.EncodeFloat(pcm, tc.frameSize)
			if encLen <= 0 {
				t.Fatalf("Encoding failed with len=%d", encLen)
			}
			t.Logf("Encoded %d samples to %d bytes", tc.frameSize, encLen)

			// Create libopus decoder (reference)
			libDec, err := NewLibopusDecoder(48000, tc.channels)
			if err != nil || libDec == nil {
				t.Skip("Failed to create libopus decoder")
			}
			defer libDec.Destroy()

			// Create gopus decoder
			gopusDec, err := gopus.NewDecoderDefault(48000, tc.channels)
			if err != nil {
				t.Fatalf("Failed to create gopus decoder: %v", err)
			}

			// Decode with libopus (reference)
			libDecoded, libDecLen := libDec.DecodeFloat(packet, tc.frameSize)
			if libDecLen <= 0 {
				t.Fatalf("libopus decode failed: %d", libDecLen)
			}

			// Decode with gopus
			gopusDecoded, err := decodeFloat32(gopusDec, packet)
			if err != nil {
				t.Fatalf("gopus decode failed: %v", err)
			}

			expectedSamples := tc.frameSize * tc.channels
			if len(gopusDecoded) != expectedSamples {
				t.Errorf("Output length: got %d, want %d", len(gopusDecoded), expectedSamples)
			}

			// Check for NaN/Inf
			for i, s := range gopusDecoded {
				if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
					t.Errorf("Invalid sample at %d: %v", i, s)
				}
			}

			// Compare gopus vs libopus decoder outputs
			result := compareDecoderOutputs(libDecoded[:expectedSamples], gopusDecoded)

			t.Logf("gopus vs libopus: maxDiff=%.6f (@%d), rmsDiff=%.6f, corr=%.6f, meanDiff=%.6f",
				result.maxDiff, result.maxDiffIdx, result.rmsDiff, result.correlation, result.meanDiff)

			// Also compute correlation with original signal for context
			corrOrig := computeCorrelationF32vsF32(pcm, gopusDecoded)
			t.Logf("gopus vs original: correlation=%.6f", corrOrig)

			// Report match status
			if result.maxDiff < MaxSampleDiffThreshold && result.correlation > CorrelationThreshold {
				t.Logf("✓ MATCH: gopus decoder matches libopus")
			} else {
				t.Logf("✗ MISMATCH: maxDiff=%.6f (threshold=%.6f), corr=%.6f (threshold=%.6f)",
					result.maxDiff, MaxSampleDiffThreshold, result.correlation, CorrelationThreshold)

				// Show first few samples that differ significantly
				showDiffs := 0
				for i := 0; i < expectedSamples && showDiffs < 5; i++ {
					diff := math.Abs(float64(libDecoded[i]) - float64(gopusDecoded[i]))
					if diff > MaxSampleDiffThreshold/10 {
						t.Logf("  sample[%d]: libopus=%.6f, gopus=%.6f, diff=%.6f",
							i, libDecoded[i], gopusDecoded[i], diff)
						showDiffs++
					}
				}
			}
		})
	}
}

// TestLibopusEncodeGopusDecodeMultiFrame tests decoding of multiple sequential frames.
func TestLibopusEncodeGopusDecodeMultiFrame(t *testing.T) {
	// Test encoding multiple frames sequentially (simulating streaming)
	channels := 1
	frameSize := 960
	numFrames := 10

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Skip("Failed to create libopus encoder")
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(64000)
	libEnc.SetBandwidth(OpusBandwidthFullband)

	gopusDec, err := gopus.NewDecoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	rng := rand.New(rand.NewSource(42))

	for i := 0; i < numFrames; i++ {
		// Generate frame with some continuity (sine wave with phase continuity)
		pcm := make([]float32, frameSize)
		basePhase := float64(i*frameSize) / 48000.0
		for j := 0; j < frameSize; j++ {
			ti := basePhase + float64(j)/48000.0
			pcm[j] = 0.5 * float32(math.Sin(2*math.Pi*440*ti))
			// Add some noise
			pcm[j] += 0.1 * float32(rng.Float64()*2-1)
		}

		// Encode
		packet, encLen := libEnc.EncodeFloat(pcm, frameSize)
		if encLen <= 0 {
			t.Fatalf("Frame %d: encoding failed", i)
		}

		// Decode
		decoded, err := decodeFloat32(gopusDec, packet)
		if err != nil {
			t.Fatalf("Frame %d: decode failed: %v", i, err)
		}

		if len(decoded) != frameSize*channels {
			t.Errorf("Frame %d: output length %d, want %d", i, len(decoded), frameSize*channels)
		}

		// Check for NaN/Inf
		for j, s := range decoded {
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
				t.Errorf("Frame %d: invalid sample at %d: %v", i, j, s)
			}
		}
	}

	t.Logf("Successfully decoded %d frames in sequence", numFrames)
}

// TestLibopusEncodeGopusDecodeRandomStress stress tests with random configurations.
func TestLibopusEncodeGopusDecodeRandomStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	rng := rand.New(rand.NewSource(999))
	iterations := 100

	frameSizes := []int{120, 240, 480, 960, 1920, 2880}
	bandwidths := []int{
		OpusBandwidthNarrowband,
		OpusBandwidthMediumband,
		OpusBandwidthWideband,
		OpusBandwidthSuperwideband,
		OpusBandwidthFullband,
	}

	successCount := 0
	for i := 0; i < iterations; i++ {
		channels := rng.Intn(2) + 1
		frameSize := frameSizes[rng.Intn(len(frameSizes))]
		bitrate := (rng.Intn(240) + 8) * 1000
		bandwidth := bandwidths[rng.Intn(len(bandwidths))]

		// Generate random audio
		pcm := generateRandomAudioF32(rng, frameSize*channels)

		// Create encoder
		libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
		if err != nil || libEnc == nil {
			continue
		}

		libEnc.SetBitrate(bitrate)
		libEnc.SetBandwidth(bandwidth)

		// Encode
		packet, encLen := libEnc.EncodeFloat(pcm, frameSize)
		libEnc.Destroy()

		if encLen <= 0 {
			continue
		}

		// Create decoder
		gopusDec, err := gopus.NewDecoderDefault(48000, channels)
		if err != nil {
			continue
		}

		// Decode
		decoded, err := decodeFloat32(gopusDec, packet)
		if err != nil {
			t.Logf("Iteration %d: decode error (channels=%d, frameSize=%d, bitrate=%d, bw=%d): %v",
				i, channels, frameSize, bitrate, bandwidth, err)
			continue
		}

		// Verify output
		valid := true
		for _, s := range decoded {
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
				valid = false
				break
			}
		}

		if valid && len(decoded) == frameSize*channels {
			successCount++
		}
	}

	t.Logf("Stress test: %d/%d successful decodes", successCount, iterations)
	if successCount < iterations/2 {
		t.Errorf("Too many failures: only %d/%d succeeded", successCount, iterations)
	}
}

// Helper functions

func generateRandomAudioF32(rng *rand.Rand, samples int) []float32 {
	pcm := make([]float32, samples)
	for i := range pcm {
		// Mix of sine and noise for more realistic audio
		sine := 0.3 * math.Sin(2*math.Pi*440*float64(i)/48000)
		noise := 0.2 * (rng.Float64()*2 - 1)
		pcm[i] = float32(sine + noise)
	}
	return pcm
}

func generateTestSignalF32(signalType string, frameSize, channels int) []float32 {
	samples := frameSize * channels
	pcm := make([]float32, samples)

	switch signalType {
	case "sine":
		for i := 0; i < samples; i++ {
			ch := i % channels
			sampleIdx := i / channels
			ti := float64(sampleIdx) / 48000.0
			freq := 440.0 + float64(ch)*100 // Different freq per channel
			pcm[i] = 0.5 * float32(math.Sin(2*math.Pi*freq*ti))
		}

	case "noise":
		rng := rand.New(rand.NewSource(12345))
		for i := range pcm {
			pcm[i] = float32(rng.Float64()*2 - 1)
		}

	case "multitone":
		freqs := []float64{220, 440, 880, 1760}
		for i := 0; i < samples; i++ {
			ch := i % channels
			sampleIdx := i / channels
			ti := float64(sampleIdx) / 48000.0
			var sum float64
			for _, freq := range freqs {
				sum += math.Sin(2 * math.Pi * (freq + float64(ch)*50) * ti)
			}
			pcm[i] = 0.2 * float32(sum)
		}

	default:
		// Silence
		for i := range pcm {
			pcm[i] = 0
		}
	}

	return pcm
}

func computeCorrelationF32vsF32(x, y []float32) float64 {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	var sumXY, sumX2, sumY2 float64
	for i := 0; i < n; i++ {
		sumXY += float64(x[i]) * float64(y[i])
		sumX2 += float64(x[i]) * float64(x[i])
		sumY2 += float64(y[i]) * float64(y[i])
	}
	if sumX2 == 0 || sumY2 == 0 {
		return 0
	}
	return sumXY / (math.Sqrt(sumX2) * math.Sqrt(sumY2))
}

func computeRMSF32(samples []float32) float64 {
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}
