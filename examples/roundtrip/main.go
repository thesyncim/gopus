// Package main demonstrates gopus encode-decode roundtrip with quality metrics.
//
// This example generates test signals, encodes them with gopus, decodes back
// to PCM, and measures quality using SNR, correlation, and peak error.
//
// Usage:
//
//	go run . -duration 1 -signal sine
//	go run . -duration 1 -signal sweep -bitrate 96000
//	go run . -all  # Test all configurations
package main

import (
	"flag"
	"fmt"
	"log"
	"math"

	"github.com/thesyncim/gopus"
)

const (
	sampleRate = 48000
	frameSize  = 960 // 20ms at 48kHz
)

// TestConfig defines a roundtrip test configuration.
type TestConfig struct {
	Name        string
	Application gopus.Application
	Bitrate     int
	Channels    int
}

func main() {
	// Parse flags
	duration := flag.Float64("duration", 1.0, "Duration in seconds")
	signal := flag.String("signal", "sine", "Signal type: sine, sweep, noise, speech")
	bitrate := flag.Int("bitrate", 64000, "Target bitrate in bps")
	channels := flag.Int("channels", 2, "Number of channels (1 or 2)")
	runAll := flag.Bool("all", false, "Run all test configurations")
	flag.Parse()

	if *runAll {
		runAllTests(*duration)
		return
	}

	// Single test with specified parameters
	config := TestConfig{
		Name:        "Custom",
		Application: gopus.ApplicationAudio,
		Bitrate:     *bitrate,
		Channels:    *channels,
	}

	fmt.Printf("=== Roundtrip Test: %s signal, %d kbps, %d ch ===\n",
		*signal, *bitrate/1000, *channels)

	original := generateSignal(*signal, *duration, *channels)
	decoded, err := roundtrip(original, config)
	if err != nil {
		log.Fatalf("Roundtrip failed: %v", err)
	}

	printQualityReport(original, decoded)
}

// Quality thresholds for pass/fail determination.
const (
	snrThreshold  = 10.0 // dB - minimum acceptable SNR
	corrThreshold = 0.9  // minimum acceptable correlation
)

// runAllTests runs a matrix of test configurations.
func runAllTests(duration float64) {
	configs := []TestConfig{
		{"VoIP 32kbps mono", gopus.ApplicationVoIP, 32000, 1},
		{"VoIP 32kbps stereo", gopus.ApplicationVoIP, 32000, 2},
		{"Audio 64kbps mono", gopus.ApplicationAudio, 64000, 1},
		{"Audio 64kbps stereo", gopus.ApplicationAudio, 64000, 2},
		{"Audio 128kbps stereo", gopus.ApplicationAudio, 128000, 2},
		{"LowDelay 64kbps stereo", gopus.ApplicationLowDelay, 64000, 2},
	}

	signals := []string{"sine", "sweep", "noise"}

	fmt.Printf("=== Roundtrip Quality Tests (%.1fs) ===\n\n", duration)
	fmt.Printf("Thresholds: SNR > %.0f dB, Correlation > %.1f\n\n", snrThreshold, corrThreshold)
	fmt.Printf("%-25s %-8s %9s %8s %10s %6s\n",
		"Config", "Signal", "SNR (dB)", "Corr", "Peak Err", "Status")
	fmt.Println(string(make([]byte, 75)))

	passed, failed := 0, 0

	for _, config := range configs {
		for _, sig := range signals {
			original := generateSignal(sig, duration, config.Channels)
			decoded, err := roundtrip(original, config)
			if err != nil {
				fmt.Printf("%-25s %-8s %9s %8s %10s %6s\n",
					config.Name, sig, "-", "-", "-", "ERROR")
				failed++
				continue
			}

			snr := calculateSNR(original, decoded)
			corr := calculateCorrelation(original, decoded)
			peakErr := calculatePeakError(original, decoded)

			// Determine pass/fail
			status := "FAIL"
			if snr > snrThreshold && math.Abs(corr) > corrThreshold {
				status = "PASS"
				passed++
			} else {
				failed++
			}

			fmt.Printf("%-25s %-8s %9.2f %8.4f %10.6f %6s\n",
				config.Name, sig, snr, corr, peakErr, status)
		}
	}

	// Summary
	fmt.Println(string(make([]byte, 75)))
	fmt.Printf("\nSummary: %d/%d tests passed (%.0f%%)\n",
		passed, passed+failed, 100*float64(passed)/float64(passed+failed))

	if failed > 0 {
		fmt.Println("\n[!] Known issue: gopus decoder quality is in development.")
		fmt.Println("    See: .planning/STATE.md 'Known Gaps' section")
		fmt.Println("    Track: github.com/thesyncim/gopus/issues")
	}
}

// generateSignal creates a test signal.
func generateSignal(signalType string, duration float64, channels int) []float32 {
	samples := int(duration * sampleRate)
	pcm := make([]float32, samples*channels)

	switch signalType {
	case "sine":
		// 440Hz sine wave
		for i := 0; i < samples; i++ {
			t := float64(i) / float64(sampleRate)
			val := float32(0.5 * math.Sin(2*math.Pi*440*t))
			for ch := 0; ch < channels; ch++ {
				pcm[i*channels+ch] = val
			}
		}

	case "sweep":
		// Frequency sweep from 100Hz to 8000Hz
		for i := 0; i < samples; i++ {
			t := float64(i) / float64(sampleRate)
			progress := t / duration
			freq := 100 + (8000-100)*progress
			phase := 2 * math.Pi * freq * t
			val := float32(0.5 * math.Sin(phase))
			for ch := 0; ch < channels; ch++ {
				pcm[i*channels+ch] = val
			}
		}

	case "noise":
		// Pseudo-random noise (deterministic for reproducibility)
		seed := uint32(12345)
		for i := 0; i < samples*channels; i++ {
			// Simple LCG
			seed = seed*1103515245 + 12345
			pcm[i] = float32((seed>>16)&0x7FFF)/32768.0 - 0.5
			pcm[i] *= 0.5 // Reduce amplitude
		}

	case "speech":
		// Simulated speech-like signal (voiced + unvoiced)
		for i := 0; i < samples; i++ {
			t := float64(i) / float64(sampleRate)
			// Mix of low frequency (voiced) and noise (unvoiced)
			voiced := float32(0.3 * math.Sin(2*math.Pi*150*t))
			// Simple noise
			seed := uint32(i + 1)
			seed = seed*1103515245 + 12345
			unvoiced := float32((seed>>16)&0x7FFF)/32768.0 - 0.5
			unvoiced *= 0.1

			val := voiced + unvoiced
			for ch := 0; ch < channels; ch++ {
				pcm[i*channels+ch] = val
			}
		}

	default:
		log.Fatalf("Unknown signal type: %s", signalType)
	}

	return pcm
}

// roundtrip encodes and decodes audio.
func roundtrip(original []float32, config TestConfig) ([]float32, error) {
	// Create encoder
	enc, err := gopus.NewEncoder(sampleRate, config.Channels, config.Application)
	if err != nil {
		return nil, fmt.Errorf("create encoder: %w", err)
	}
	if err := enc.SetBitrate(config.Bitrate); err != nil {
		return nil, fmt.Errorf("set bitrate: %w", err)
	}

	// Create decoder
	dec, err := gopus.NewDecoderDefault(sampleRate, config.Channels)
	if err != nil {
		return nil, fmt.Errorf("create decoder: %w", err)
	}

	// Process in frames
	frameSamples := frameSize * config.Channels
	numFrames := len(original) / frameSamples
	decoded := make([]float32, 0, len(original))

	for i := 0; i < numFrames; i++ {
		start := i * frameSamples
		end := start + frameSamples
		frame := original[start:end]

		// Encode
		packet, err := enc.EncodeFloat32(frame)
		if err != nil {
			return nil, fmt.Errorf("encode frame %d: %w", i, err)
		}

		// Decode
		samples, err := dec.DecodeFloat32(packet)
		if err != nil {
			return nil, fmt.Errorf("decode frame %d: %w", i, err)
		}

		decoded = append(decoded, samples...)
	}

	return decoded, nil
}

// printQualityReport prints detailed quality metrics.
func printQualityReport(original, decoded []float32) {
	fmt.Println("\n--- Quality Report ---")

	// Ensure same length for comparison
	minLen := len(original)
	if len(decoded) < minLen {
		minLen = len(decoded)
	}
	original = original[:minLen]
	decoded = decoded[:minLen]

	// Calculate metrics
	snr := calculateSNR(original, decoded)
	corr := calculateCorrelation(original, decoded)
	peakErr := calculatePeakError(original, decoded)
	mse := calculateMSE(original, decoded)
	origEnergy := calculateEnergy(original)
	decEnergy := calculateEnergy(decoded)

	fmt.Printf("Samples compared: %d\n", minLen)
	fmt.Printf("\nSignal Quality:\n")
	fmt.Printf("  SNR:              %8.2f dB\n", snr)
	fmt.Printf("  Correlation:      %8.4f\n", corr)
	fmt.Printf("  Peak Error:       %8.6f\n", peakErr)
	fmt.Printf("  MSE:              %8.6f\n", mse)
	fmt.Printf("\nEnergy:\n")
	fmt.Printf("  Original:         %8.4f\n", origEnergy)
	fmt.Printf("  Decoded:          %8.4f\n", decEnergy)
	fmt.Printf("  Ratio:            %8.4f (%.1f%%)\n",
		decEnergy/origEnergy, 100*decEnergy/origEnergy)

	// Quality assessment
	fmt.Printf("\nAssessment: ")
	switch {
	case snr > 30:
		fmt.Println("Excellent (transparent)")
	case snr > 20:
		fmt.Println("Good (minor artifacts)")
	case snr > 10:
		fmt.Println("Fair (noticeable artifacts)")
	case snr > 0:
		fmt.Println("Poor (significant distortion)")
	default:
		fmt.Println("Very poor (severe distortion)")
	}
}

// calculateSNR computes Signal-to-Noise Ratio in dB.
func calculateSNR(original, decoded []float32) float64 {
	var signal, noise float64
	for i := range original {
		signal += float64(original[i] * original[i])
		diff := original[i] - decoded[i]
		noise += float64(diff * diff)
	}
	if noise == 0 {
		return math.Inf(1)
	}
	if signal == 0 {
		return math.Inf(-1)
	}
	return 10 * math.Log10(signal / noise)
}

// calculateCorrelation computes Pearson correlation coefficient.
func calculateCorrelation(original, decoded []float32) float64 {
	n := float64(len(original))
	if n == 0 {
		return 0
	}

	// Calculate means
	var sumOrig, sumDec float64
	for i := range original {
		sumOrig += float64(original[i])
		sumDec += float64(decoded[i])
	}
	meanOrig := sumOrig / n
	meanDec := sumDec / n

	// Calculate correlation
	var cov, varOrig, varDec float64
	for i := range original {
		dOrig := float64(original[i]) - meanOrig
		dDec := float64(decoded[i]) - meanDec
		cov += dOrig * dDec
		varOrig += dOrig * dOrig
		varDec += dDec * dDec
	}

	if varOrig == 0 || varDec == 0 {
		return 0
	}
	return cov / math.Sqrt(varOrig*varDec)
}

// calculatePeakError finds the maximum absolute difference.
func calculatePeakError(original, decoded []float32) float64 {
	var peak float64
	for i := range original {
		diff := math.Abs(float64(original[i] - decoded[i]))
		if diff > peak {
			peak = diff
		}
	}
	return peak
}

// calculateMSE computes Mean Squared Error.
func calculateMSE(original, decoded []float32) float64 {
	var sum float64
	for i := range original {
		diff := float64(original[i] - decoded[i])
		sum += diff * diff
	}
	return sum / float64(len(original))
}

// calculateEnergy computes signal energy.
func calculateEnergy(samples []float32) float64 {
	var sum float64
	for _, s := range samples {
		sum += float64(s * s)
	}
	return sum / float64(len(samples))
}
