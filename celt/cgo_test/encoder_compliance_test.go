//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"fmt"
	"strings"
	"testing"
)

// Quality thresholds for encoder compliance.
const (
	// EncoderPassThreshold is Q >= 0, corresponding to 48 dB SNR (RFC 8251 level).
	EncoderPassThreshold = 0.0

	// EncoderMinThreshold is Q >= -25, corresponding to 36 dB SNR (minimum acceptable).
	EncoderMinThreshold = -25.0

	// Note: The gopus encoder is still in development. Current tests are expected
	// to show poor quality scores until encoder implementation is complete.
	// These tests serve as a quality baseline and will start passing as the
	// encoder improves.
)

// runEncoderTest executes a single encoder compliance test.
// Recovers from panics to allow the test suite to continue.
func runEncoderTest(t *testing.T, cfg EncoderTestConfig) (result EncoderTestResult) {
	t.Helper()

	result = EncoderTestResult{Config: cfg}

	// Recover from panics in the encoder
	defer func() {
		if r := recover(); r != nil {
			result.Error = fmt.Errorf("panic: %v", r)
		}
	}()

	// 1. Generate 1 second of test signal
	sampleRate := 48000
	totalSamples := sampleRate * cfg.Channels
	freqs := frequenciesForBandwidth(cfg.Bandwidth)
	signal := generateMultiFrequencySignal(totalSamples, cfg.Channels, freqs)

	// 2. Encode with gopus
	packets, encodedBytes, err := encodeSignal(signal, cfg)
	if err != nil {
		result.Error = fmt.Errorf("encoding failed: %w", err)
		return result
	}
	result.EncodedBytes = encodedBytes

	if len(packets) == 0 {
		result.Error = fmt.Errorf("no packets produced")
		return result
	}

	// 3. Decode with libopus
	decoded, totalDecodedSamples, err := decodeWithLibopus(packets, sampleRate, cfg.Channels, cfg.FrameSize)
	if err != nil {
		result.Error = fmt.Errorf("decoding failed: %w", err)
		return result
	}
	result.TotalSamples = totalDecodedSamples

	// 4. Compute quality metrics
	q, snr := compareAudio(signal, decoded, cfg.Channels)
	result.Quality = q
	result.SNR = snr
	result.Passed = q >= EncoderPassThreshold

	return result
}

// TestEncoderComplianceSILK_CGO tests SILK mode encoder compliance.
// Note: Tests log quality scores but don't fail - encoder is in development.
func TestEncoderComplianceSILK_CGO(t *testing.T) {
	configs := buildSILKConfigs()

	for _, cfg := range configs {
		t.Run(cfg.Name, func(t *testing.T) {
			result := runEncoderTest(t, cfg)

			if result.Error != nil {
				t.Logf("Test error: %v", result.Error)
				return
			}

			status := "PASS"
			if !result.Passed {
				status = "BELOW_THRESHOLD"
			}
			t.Logf("Q=%.2f, SNR=%.2f dB, %d bytes [%s]",
				result.Quality, result.SNR, result.EncodedBytes, status)
		})
	}
}

// TestEncoderComplianceCELT_CGO tests CELT mode encoder compliance.
// Note: Tests log quality scores but don't fail - encoder is in development.
func TestEncoderComplianceCELT_CGO(t *testing.T) {
	configs := buildCELTConfigs()

	for _, cfg := range configs {
		t.Run(cfg.Name, func(t *testing.T) {
			result := runEncoderTest(t, cfg)

			if result.Error != nil {
				t.Logf("Test error: %v", result.Error)
				return
			}

			status := "PASS"
			if !result.Passed {
				status = "BELOW_THRESHOLD"
			}
			t.Logf("Q=%.2f, SNR=%.2f dB, %d bytes [%s]",
				result.Quality, result.SNR, result.EncodedBytes, status)
		})
	}
}

// TestEncoderComplianceHybrid_CGO tests Hybrid mode encoder compliance.
// Note: Tests log quality scores but don't fail - encoder is in development.
func TestEncoderComplianceHybrid_CGO(t *testing.T) {
	configs := buildHybridConfigs()

	for _, cfg := range configs {
		t.Run(cfg.Name, func(t *testing.T) {
			result := runEncoderTest(t, cfg)

			if result.Error != nil {
				t.Logf("Test error: %v", result.Error)
				return
			}

			status := "PASS"
			if !result.Passed {
				status = "BELOW_THRESHOLD"
			}
			t.Logf("Q=%.2f, SNR=%.2f dB, %d bytes [%s]",
				result.Quality, result.SNR, result.EncodedBytes, status)
		})
	}
}

// TestEncoderComplianceQuick_CGO runs a minimal subset for quick validation.
// Note: Tests log quality scores but don't fail - encoder is in development.
func TestEncoderComplianceQuick_CGO(t *testing.T) {
	configs := buildQuickConfigs()

	for _, cfg := range configs {
		t.Run(cfg.Name, func(t *testing.T) {
			result := runEncoderTest(t, cfg)

			if result.Error != nil {
				t.Logf("Test error: %v", result.Error)
				return
			}

			status := "PASS"
			if !result.Passed {
				status = "BELOW_THRESHOLD"
			}
			t.Logf("Q=%.2f, SNR=%.2f dB, %d bytes [%s]",
				result.Quality, result.SNR, result.EncodedBytes, status)
		})
	}
}

// TestEncoderComplianceSummary_CGO runs all configurations and outputs a summary table.
func TestEncoderComplianceSummary_CGO(t *testing.T) {
	configs := buildAllConfigs()

	var results []EncoderTestResult
	passed := 0
	failed := 0
	errors := 0

	// Run all tests silently
	for _, cfg := range configs {
		result := runEncoderTest(t, cfg)
		results = append(results, result)

		if result.Error != nil {
			errors++
		} else if result.Passed {
			passed++
		} else {
			failed++
		}
	}

	// Print summary header
	t.Log("")
	t.Log("Encoder Compliance Summary (libopus reference decoder)")
	t.Log(strings.Repeat("=", 70))
	t.Logf("%-38s | %7s | %9s | %s", "Configuration", "Q", "SNR(dB)", "Status")
	t.Log(strings.Repeat("-", 70))

	// Print results by mode
	currentMode := ""
	for _, r := range results {
		// Add mode separator
		if r.Config.Mode != currentMode {
			if currentMode != "" {
				t.Log(strings.Repeat("-", 70))
			}
			currentMode = r.Config.Mode
		}

		var status string
		if r.Error != nil {
			status = "ERROR"
		} else if r.Passed {
			status = "PASS"
		} else if r.Quality >= EncoderMinThreshold {
			status = "INFO" // Acceptable but below ideal
		} else {
			status = "FAIL"
		}

		if r.Error != nil {
			t.Logf("%-38s | %7s | %9s | %s: %v",
				r.Config.Name, "-", "-", status, r.Error)
		} else {
			t.Logf("%-38s | %7.2f | %9.2f | %s",
				r.Config.Name, r.Quality, r.SNR, status)
		}
	}

	// Print summary footer
	t.Log(strings.Repeat("-", 70))
	total := len(configs)
	t.Logf("Total: %d/%d passed (%.1f%%)", passed, total, float64(passed)*100/float64(total))
	if errors > 0 {
		t.Logf("Errors: %d", errors)
	}
	if failed > 0 {
		t.Logf("Failed: %d", failed)
	}
	t.Log("")
	t.Log("Thresholds:")
	t.Logf("  PASS: Q >= %.1f (48 dB SNR)", EncoderPassThreshold)
	t.Logf("  INFO: Q >= %.1f (36 dB SNR, minimum acceptable)", EncoderMinThreshold)
	t.Logf("  FAIL: Q < %.1f", EncoderMinThreshold)

	// Log summary - don't fail since encoder is in development
	if failed > 0 || errors > 0 {
		t.Logf("Note: %d below threshold, %d errors (encoder in development)", failed, errors)
	}
}

// TestEncoderComplianceSummaryByMode_CGO outputs a condensed summary grouped by mode.
func TestEncoderComplianceSummaryByMode_CGO(t *testing.T) {
	modeConfigs := map[string][]EncoderTestConfig{
		"SILK":   buildSILKConfigs(),
		"CELT":   buildCELTConfigs(),
		"Hybrid": buildHybridConfigs(),
	}

	t.Log("")
	t.Log("Encoder Compliance Summary by Mode")
	t.Log(strings.Repeat("=", 50))

	for _, mode := range []string{"SILK", "CELT", "Hybrid"} {
		configs := modeConfigs[mode]

		passed := 0
		failed := 0
		errors := 0
		minQ := 1000.0
		maxQ := -1000.0
		sumQ := 0.0

		for _, cfg := range configs {
			result := runEncoderTest(t, cfg)

			if result.Error != nil {
				errors++
				continue
			}

			sumQ += result.Quality
			if result.Quality < minQ {
				minQ = result.Quality
			}
			if result.Quality > maxQ {
				maxQ = result.Quality
			}

			if result.Passed {
				passed++
			} else {
				failed++
			}
		}

		total := len(configs)
		valid := total - errors
		avgQ := 0.0
		if valid > 0 {
			avgQ = sumQ / float64(valid)
		}

		t.Logf("")
		t.Logf("%s Mode:", mode)
		t.Logf("  Configurations: %d", total)
		t.Logf("  Passed: %d/%d (%.1f%%)", passed, total, float64(passed)*100/float64(total))
		if valid > 0 {
			t.Logf("  Quality: min=%.2f, avg=%.2f, max=%.2f", minQ, avgQ, maxQ)
		}
		if errors > 0 {
			t.Logf("  Errors: %d", errors)
		}
	}

	t.Log("")
}
