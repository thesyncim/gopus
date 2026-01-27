package cgo

import (
	"fmt"
	"math"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/testvectors"
)

// EncoderTestConfig defines a single encoder compliance test case.
type EncoderTestConfig struct {
	Name      string // Human-readable name (e.g., "SILK-NB-20ms-mono-12k")
	Mode      string // "SILK", "CELT", "Hybrid"
	Bandwidth string // "NB", "MB", "WB", "SWB", "FB"
	FrameSize int    // samples at 48kHz: 120, 240, 480, 960, 1920, 2880
	Channels  int    // 1 or 2
	Bitrate   int    // target bitrate in bps
}

// EncoderTestResult holds the outcome of a single test.
type EncoderTestResult struct {
	Config       EncoderTestConfig
	Quality      float64 // Q metric (Q >= 0 means 48 dB SNR)
	SNR          float64 // Signal-to-noise ratio in dB
	Passed       bool
	TotalSamples int
	EncodedBytes int
	Error        error
}

// Pre-skip for Opus decoder (312 samples at 48kHz per channel).
const opusPreSkip = 312

// frameSizeToMs converts frame size in samples to milliseconds.
func frameSizeToMs(frameSize int) float64 {
	return float64(frameSize) * 1000.0 / 48000.0
}

// frequenciesForBandwidth returns test frequencies appropriate for the bandwidth.
// These frequencies span the usable range for each bandwidth setting.
func frequenciesForBandwidth(bandwidth string) []float64 {
	switch bandwidth {
	case "NB": // Narrowband: up to 4kHz audio
		return []float64{200, 400, 800}
	case "MB": // Mediumband: up to 6kHz audio
		return []float64{200, 500, 1200}
	case "WB": // Wideband: up to 8kHz audio
		return []float64{300, 800, 2000}
	case "SWB": // Superwideband: up to 12kHz audio
		return []float64{400, 1000, 3000, 6000}
	case "FB": // Fullband: up to 20kHz audio
		return []float64{440, 1000, 2000, 5000, 10000}
	default:
		return []float64{440, 1000, 2000}
	}
}

// generateMultiFrequencySignal creates a test signal with multiple sine waves.
// Each frequency has amplitude 0.3 to avoid clipping when summed.
func generateMultiFrequencySignal(totalSamples, channels int, freqs []float64) []float32 {
	sampleRate := 48000.0
	samplesPerChannel := totalSamples / channels
	amplitude := 0.3 / float64(len(freqs)) // Normalize to avoid clipping

	signal := make([]float32, totalSamples)

	for i := 0; i < samplesPerChannel; i++ {
		t := float64(i) / sampleRate
		var sample float64
		for _, freq := range freqs {
			sample += amplitude * math.Sin(2.0*math.Pi*freq*t)
		}

		if channels == 1 {
			signal[i] = float32(sample)
		} else {
			// Stereo: slight frequency offset on right channel for differentiation
			var sampleR float64
			for _, freq := range freqs {
				sampleR += amplitude * math.Sin(2.0*math.Pi*freq*1.01*t)
			}
			signal[i*2] = float32(sample)
			signal[i*2+1] = float32(sampleR)
		}
	}

	return signal
}

// getApplicationForMode returns the appropriate gopus Application for the mode.
func getApplicationForMode(mode string) gopus.Application {
	switch mode {
	case "SILK":
		return gopus.ApplicationVoIP
	case "CELT":
		return gopus.ApplicationAudio
	case "Hybrid":
		return gopus.ApplicationAudio
	default:
		return gopus.ApplicationAudio
	}
}

// encodeSignal encodes a signal using gopus encoder with given configuration.
func encodeSignal(signal []float32, cfg EncoderTestConfig) ([][]byte, int, error) {
	enc, err := gopus.NewEncoder(48000, cfg.Channels, getApplicationForMode(cfg.Mode))
	if err != nil {
		return nil, 0, fmt.Errorf("create encoder: %w", err)
	}

	enc.SetBitrate(cfg.Bitrate)
	enc.SetFrameSize(cfg.FrameSize)

	var packets [][]byte
	var totalBytes int
	frameLen := cfg.FrameSize * cfg.Channels

	// Encode frame by frame
	for offset := 0; offset+frameLen <= len(signal); offset += frameLen {
		frame := signal[offset : offset+frameLen]
		data := make([]byte, 1275) // max Opus packet size

		n, err := enc.Encode(frame, data)
		if err != nil {
			return nil, 0, fmt.Errorf("encode frame at offset %d: %w", offset, err)
		}

		packets = append(packets, data[:n])
		totalBytes += n
	}

	return packets, totalBytes, nil
}

// decodeWithLibopus decodes packets using the libopus CGO decoder.
func decodeWithLibopus(packets [][]byte, sampleRate, channels, frameSize int) ([]float32, int, error) {
	dec, err := NewLibopusDecoder(sampleRate, channels)
	if err != nil {
		return nil, 0, fmt.Errorf("create libopus decoder: %w", err)
	}
	defer dec.Destroy()

	var decoded []float32
	totalSamples := 0

	for i, packet := range packets {
		// DecodeFloat returns samples-per-channel, buffer is always stereo-sized
		samples, samplesPerChannel := dec.DecodeFloat(packet, frameSize)
		if samplesPerChannel <= 0 {
			return nil, 0, fmt.Errorf("decode packet %d failed: %d", i, samplesPerChannel)
		}
		// Take samplesPerChannel * channels from the buffer
		totalInBuffer := samplesPerChannel * channels
		decoded = append(decoded, samples[:totalInBuffer]...)
		totalSamples += totalInBuffer
	}

	return decoded, totalSamples, nil
}

// compareAudio computes quality metrics between original and decoded audio.
// Handles pre-skip alignment and length differences.
// Uses correlation-based alignment to find the best match position.
func compareAudio(original, decoded []float32, channels int) (q, snr float64) {
	// Strip pre-skip from decoded
	preSkipSamples := opusPreSkip * channels
	if len(decoded) > preSkipSamples {
		decoded = decoded[preSkipSamples:]
	}

	if len(original) == 0 || len(decoded) == 0 {
		return math.Inf(-1), math.Inf(-1)
	}

	// Find best alignment using cross-correlation
	// Search within a reasonable window (up to 2 frames worth of delay)
	maxSearchDelay := 960 * 2 * channels // 2 frames at 20ms
	if maxSearchDelay > len(decoded)/4 {
		maxSearchDelay = len(decoded) / 4
	}

	bestOffset := 0
	bestCorr := math.Inf(-1)

	// Try different offsets
	for offset := 0; offset <= maxSearchDelay; offset += channels {
		compareLen := len(original)
		if len(decoded)-offset < compareLen {
			compareLen = len(decoded) - offset
		}
		if compareLen <= 0 {
			continue
		}

		// Compute normalized correlation
		var sumXY, sumXX, sumYY float64
		for i := 0; i < compareLen; i++ {
			x := float64(original[i])
			y := float64(decoded[offset+i])
			sumXY += x * y
			sumXX += x * x
			sumYY += y * y
		}

		if sumXX > 0 && sumYY > 0 {
			corr := sumXY / math.Sqrt(sumXX*sumYY)
			if corr > bestCorr {
				bestCorr = corr
				bestOffset = offset
			}
		}
	}

	// Use best offset for final comparison
	compareLen := len(original)
	if len(decoded)-bestOffset < compareLen {
		compareLen = len(decoded) - bestOffset
	}

	if compareLen <= 0 {
		return math.Inf(-1), math.Inf(-1)
	}

	// Use existing quality metrics with aligned signals
	q = testvectors.ComputeQualityFloat32(decoded[bestOffset:bestOffset+compareLen], original[:compareLen], 48000)
	snr = testvectors.SNRFromQuality(q)

	return q, snr
}

// buildSILKConfigs returns test configurations for SILK mode.
func buildSILKConfigs() []EncoderTestConfig {
	var configs []EncoderTestConfig

	bandwidths := []string{"NB", "MB", "WB"}
	frameSizes := []int{480, 960, 1920, 2880} // 10, 20, 40, 60ms
	channelOpts := []int{1, 2}
	bitrates := map[string][]int{
		"NB": {12000, 24000},
		"MB": {16000, 24000},
		"WB": {24000, 32000},
	}

	for _, bw := range bandwidths {
		for _, fs := range frameSizes {
			for _, ch := range channelOpts {
				for _, br := range bitrates[bw] {
					chStr := "mono"
					if ch == 2 {
						chStr = "stereo"
					}
					name := fmt.Sprintf("SILK-%s-%.0fms-%s-%dk",
						bw, frameSizeToMs(fs), chStr, br/1000)

					configs = append(configs, EncoderTestConfig{
						Name:      name,
						Mode:      "SILK",
						Bandwidth: bw,
						FrameSize: fs,
						Channels:  ch,
						Bitrate:   br,
					})
				}
			}
		}
	}

	return configs
}

// buildCELTConfigs returns test configurations for CELT mode.
func buildCELTConfigs() []EncoderTestConfig {
	var configs []EncoderTestConfig

	frameSizes := []int{120, 240, 480, 960} // 2.5, 5, 10, 20ms
	channelOpts := []int{1, 2}
	bitrates := []int{64000, 128000}

	for _, fs := range frameSizes {
		for _, ch := range channelOpts {
			for _, br := range bitrates {
				chStr := "mono"
				if ch == 2 {
					chStr = "stereo"
				}
				name := fmt.Sprintf("CELT-FB-%.1fms-%s-%dk",
					frameSizeToMs(fs), chStr, br/1000)

				configs = append(configs, EncoderTestConfig{
					Name:      name,
					Mode:      "CELT",
					Bandwidth: "FB",
					FrameSize: fs,
					Channels:  ch,
					Bitrate:   br,
				})
			}
		}
	}

	return configs
}

// buildHybridConfigs returns test configurations for Hybrid mode.
func buildHybridConfigs() []EncoderTestConfig {
	var configs []EncoderTestConfig

	bandwidths := []string{"SWB", "FB"}
	frameSizes := []int{480, 960} // 10, 20ms only for Hybrid
	channelOpts := []int{1, 2}
	bitrates := map[string][]int{
		"SWB": {48000, 64000},
		"FB":  {64000, 96000},
	}

	for _, bw := range bandwidths {
		for _, fs := range frameSizes {
			for _, ch := range channelOpts {
				for _, br := range bitrates[bw] {
					chStr := "mono"
					if ch == 2 {
						chStr = "stereo"
					}
					name := fmt.Sprintf("Hybrid-%s-%.0fms-%s-%dk",
						bw, frameSizeToMs(fs), chStr, br/1000)

					configs = append(configs, EncoderTestConfig{
						Name:      name,
						Mode:      "Hybrid",
						Bandwidth: bw,
						FrameSize: fs,
						Channels:  ch,
						Bitrate:   br,
					})
				}
			}
		}
	}

	return configs
}

// buildAllConfigs returns all test configurations across all modes.
func buildAllConfigs() []EncoderTestConfig {
	var all []EncoderTestConfig
	all = append(all, buildSILKConfigs()...)
	all = append(all, buildCELTConfigs()...)
	all = append(all, buildHybridConfigs()...)
	return all
}

// buildQuickConfigs returns a minimal subset for quick validation.
func buildQuickConfigs() []EncoderTestConfig {
	return []EncoderTestConfig{
		{Name: "SILK-WB-20ms-mono-24k", Mode: "SILK", Bandwidth: "WB", FrameSize: 960, Channels: 1, Bitrate: 24000},
		{Name: "CELT-FB-20ms-mono-64k", Mode: "CELT", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 64000},
		{Name: "CELT-FB-20ms-stereo-128k", Mode: "CELT", Bandwidth: "FB", FrameSize: 960, Channels: 2, Bitrate: 128000},
		{Name: "Hybrid-FB-20ms-mono-64k", Mode: "Hybrid", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 64000},
	}
}
