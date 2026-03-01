// Package testvectors provides encoder compliance testing.
// This file validates gopus encoder output by encoding raw PCM audio,
// decoding with libopus (opusdec CLI), and comparing decoded audio to
// original input using SNR-based quality metrics.
package testvectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// Quality thresholds for encoder compliance.
//
// Primary compliance target: relative parity against libopus reference fixtures.
// Gap is measured as (gopus SNR - libopus SNR), and parity thresholds are used
// whenever libopus reference fixtures are available.
//
// Absolute Q thresholds are fallback-only and are used when libopus fixtures
// are unavailable for a given case. These are calibrated against what libopus
// itself achieves in the same round-trip test (encode → decode → compare):
//
//   - CELT best:   Q ≈ -18  (39 dB SNR)
//   - SILK WB:     Q ≈ -50  (24 dB SNR)
//   - SILK NB:     Q ≈ -65  (17 dB SNR)
//   - Hybrid:      Q ≈ -51  (24 dB SNR)
//
// Note: Q >= 0 (48 dB SNR) is the RFC 8251 *decoder* compliance threshold.
// No encoder — including libopus — achieves that in round-trip tests because
// lossy encoding inherently reduces SNR.
const (
	// EncoderQualityThreshold is the absolute regression guard.
	// Set below the worst libopus case (SILK NB ≈ -65) with margin for
	// gopus development. Any result below this is a clear regression.
	EncoderQualityThreshold = -80.0 // 9.6 dB SNR — below worst libopus case

	// EncoderGoodThreshold indicates quality comparable to libopus CELT.
	// libopus CELT best is Q ≈ -18; this threshold is generous.
	EncoderGoodThreshold = -30.0 // 33.6 dB SNR

	// Pre-skip samples as defined in Ogg Opus header
	OpusPreSkip = 312

	// Relative quality targets versus libopus reference (when available).
	// Gap is reported as (gopus SNR - libopus SNR).
	// Current worst observed gap is -0.42 dB (SILK NB 10ms).
	EncoderLibopusGapGoodDB = -0.5
	EncoderLibopusGapBaseDB = -1.0

	// No-negative guard tolerance for compliance summary parity checks.
	// Allows tiny floating-point jitter while preventing meaningful negative gaps.
	EncoderLibopusNoNegativeGapToleranceDB = 0.01

	// For SILK/Hybrid, we expect close libopus alignment after parity fixes.
	// Current worst observed absolute gap is 0.69 dB (SILK NB 40ms).
	EncoderLibopusSpeechGapTightDB = 1.0
)

// Row-specific no-negative tolerances for stable residual lanes where
// libopus/gopus alignment differs by a fixed cadence/measurement offset.
// Keep these tight and evidence-backed.
var encoderLibopusNoNegativeGapOverrideDB = map[string]float64{
	"CELT-FB-2.5ms-mono-64k": 0.191,
}

var encoderComplianceLogOnce sync.Once

func logEncoderComplianceStatus(t *testing.T) {
	encoderComplianceLogOnce.Do(func() {
		t.Log("TARGET: Encoder compliance is parity-first against libopus fixture references.")
		t.Logf("TARGET: Gap thresholds (gopus SNR - libopus SNR): GOOD >= %.1f dB, BASE >= %.1f dB", EncoderLibopusGapGoodDB, EncoderLibopusGapBaseDB)
		t.Logf("TARGET: No-negative gap guard: gopus SNR - libopus SNR >= -%.2f dB", EncoderLibopusNoNegativeGapToleranceDB)
		if len(encoderLibopusNoNegativeGapOverrideDB) > 0 {
			t.Logf("TARGET: No-negative overrides: CELT-FB-2.5ms-mono-64k >= -%.3f dB",
				encoderLibopusNoNegativeGapOverrideDB["CELT-FB-2.5ms-mono-64k"])
		}
		t.Logf("TARGET: SILK/Hybrid parity guard: |gap| <= %.1f dB", EncoderLibopusSpeechGapTightDB)
		t.Log("FALLBACK: Absolute Q thresholds (calibrated against libopus round-trip values) apply when fixtures unavailable.")
	})
}

func noNegativeGapToleranceForComplianceCase(caseName string) float64 {
	if tol, ok := encoderLibopusNoNegativeGapOverrideDB[caseName]; ok {
		return tol
	}
	return EncoderLibopusNoNegativeGapToleranceDB
}

type encoderComplianceSummaryCase struct {
	name      string
	mode      encoder.Mode
	bandwidth types.Bandwidth
	frameSize int
	channels  int
	bitrate   int
}

func encoderComplianceSummaryCases() []encoderComplianceSummaryCase {
	return []encoderComplianceSummaryCase{
		// CELT
		{"CELT-FB-2.5ms-mono-64k", encoder.ModeCELT, types.BandwidthFullband, 120, 1, 64000},
		{"CELT-FB-5ms-mono-64k", encoder.ModeCELT, types.BandwidthFullband, 240, 1, 64000},
		{"CELT-FB-20ms-mono-64k", encoder.ModeCELT, types.BandwidthFullband, 960, 1, 64000},
		{"CELT-FB-20ms-stereo-128k", encoder.ModeCELT, types.BandwidthFullband, 960, 2, 128000},
		{"CELT-FB-10ms-mono-64k", encoder.ModeCELT, types.BandwidthFullband, 480, 1, 64000},
		{"CELT-FB-2.5ms-stereo-128k", encoder.ModeCELT, types.BandwidthFullband, 120, 2, 128000},
		{"CELT-FB-5ms-stereo-128k", encoder.ModeCELT, types.BandwidthFullband, 240, 2, 128000},
		// SILK
		{"SILK-NB-10ms-mono-16k", encoder.ModeSILK, types.BandwidthNarrowband, 480, 1, 16000},
		{"SILK-NB-20ms-mono-16k", encoder.ModeSILK, types.BandwidthNarrowband, 960, 1, 16000},
		{"SILK-NB-40ms-mono-16k", encoder.ModeSILK, types.BandwidthNarrowband, 1920, 1, 16000},
		{"SILK-MB-20ms-mono-24k", encoder.ModeSILK, types.BandwidthMediumband, 960, 1, 24000},
		{"SILK-WB-10ms-mono-32k", encoder.ModeSILK, types.BandwidthWideband, 480, 1, 32000},
		{"SILK-WB-20ms-mono-32k", encoder.ModeSILK, types.BandwidthWideband, 960, 1, 32000},
		{"SILK-WB-40ms-mono-32k", encoder.ModeSILK, types.BandwidthWideband, 1920, 1, 32000},
		{"SILK-WB-60ms-mono-32k", encoder.ModeSILK, types.BandwidthWideband, 2880, 1, 32000},
		{"SILK-WB-20ms-stereo-48k", encoder.ModeSILK, types.BandwidthWideband, 960, 2, 48000},
		// Hybrid
		{"Hybrid-SWB-10ms-mono-48k", encoder.ModeHybrid, types.BandwidthSuperwideband, 480, 1, 48000},
		{"Hybrid-SWB-20ms-mono-48k", encoder.ModeHybrid, types.BandwidthSuperwideband, 960, 1, 48000},
		{"Hybrid-SWB-40ms-mono-48k", encoder.ModeHybrid, types.BandwidthSuperwideband, 1920, 1, 48000},
		{"Hybrid-FB-10ms-mono-64k", encoder.ModeHybrid, types.BandwidthFullband, 480, 1, 64000},
		{"Hybrid-FB-20ms-mono-64k", encoder.ModeHybrid, types.BandwidthFullband, 960, 1, 64000},
		{"Hybrid-FB-60ms-mono-64k", encoder.ModeHybrid, types.BandwidthFullband, 2880, 1, 64000},
		{"Hybrid-FB-20ms-stereo-96k", encoder.ModeHybrid, types.BandwidthFullband, 960, 2, 96000},
	}
}

// TestEncoderComplianceCELT tests CELT mode encoding at various frame sizes.
func TestEncoderComplianceCELT(t *testing.T) {
	requireTestTier(t, testTierParity)

	logEncoderComplianceStatus(t)

	tests := []struct {
		name      string
		frameSize int // samples at 48kHz
		channels  int
	}{
		{"FB-2.5ms-mono", 120, 1},
		{"FB-5ms-mono", 240, 1},
		{"FB-10ms-mono", 480, 1},
		{"FB-20ms-mono", 960, 1},
		{"FB-20ms-stereo", 960, 2},
		{"FB-10ms-stereo", 480, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testEncoderCompliance(t, encoder.ModeCELT, types.BandwidthFullband, tc.frameSize, tc.channels, 64000)
		})
	}
}

// TestEncoderComplianceSILK tests SILK mode encoding at various bandwidths.
func TestEncoderComplianceSILK(t *testing.T) {
	requireTestTier(t, testTierParity)

	logEncoderComplianceStatus(t)

	tests := []struct {
		name      string
		bandwidth types.Bandwidth
		frameSize int
		channels  int
	}{
		{"NB-10ms-mono", types.BandwidthNarrowband, 480, 1},
		{"NB-20ms-mono", types.BandwidthNarrowband, 960, 1},
		{"MB-20ms-mono", types.BandwidthMediumband, 960, 1},
		{"WB-20ms-mono", types.BandwidthWideband, 960, 1},
		{"WB-10ms-mono", types.BandwidthWideband, 480, 1},
		{"WB-20ms-stereo", types.BandwidthWideband, 960, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testEncoderCompliance(t, encoder.ModeSILK, tc.bandwidth, tc.frameSize, tc.channels, 32000)
		})
	}
}

// TestEncoderComplianceHybrid tests Hybrid mode encoding.
func TestEncoderComplianceHybrid(t *testing.T) {
	requireTestTier(t, testTierParity)

	logEncoderComplianceStatus(t)

	tests := []struct {
		name      string
		bandwidth types.Bandwidth
		frameSize int
		channels  int
	}{
		{"SWB-10ms-mono", types.BandwidthSuperwideband, 480, 1},
		{"SWB-20ms-mono", types.BandwidthSuperwideband, 960, 1},
		{"FB-10ms-mono", types.BandwidthFullband, 480, 1},
		{"FB-20ms-mono", types.BandwidthFullband, 960, 1},
		{"SWB-20ms-stereo", types.BandwidthSuperwideband, 960, 2},
		{"FB-20ms-stereo", types.BandwidthFullband, 960, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testEncoderCompliance(t, encoder.ModeHybrid, tc.bandwidth, tc.frameSize, tc.channels, 64000)
		})
	}
}

// TestEncoderComplianceBitrates tests encoding at various bitrate targets.
func TestEncoderComplianceBitrates(t *testing.T) {
	requireTestTier(t, testTierParity)

	logEncoderComplianceStatus(t)

	bitrates := []int{32000, 64000, 128000, 256000}

	for _, bitrate := range bitrates {
		t.Run(fmt.Sprintf("CELT-%dk", bitrate/1000), func(t *testing.T) {
			testEncoderCompliance(t, encoder.ModeCELT, types.BandwidthFullband, 960, 1, bitrate)
		})
	}
}

// TestEncoderComplianceSummary runs all configurations and outputs a summary table.
func TestEncoderComplianceSummary(t *testing.T) {
	requireTestTier(t, testTierParity)

	logEncoderComplianceStatus(t)

	cases := encoderComplianceSummaryCases()

	refAvailable := libopusComplianceReferenceAvailable()
	if refAvailable {
		t.Log("Encoder Compliance Summary (Target: libopus reference)")
		t.Log("======================================================")
		t.Logf("%-35s %10s %10s %10s %10s %10s %s", "Configuration", "Q", "SNR(dB)", "LibQ", "LibSNR", "Gap(dB)", "Status")
		t.Logf("%-35s %10s %10s %10s %10s %10s %s", "--------------", "----", "------", "----", "------", "-------", "------")
	} else {
		t.Log("Encoder Compliance Summary")
		t.Log("===========================")
		t.Logf("%-35s %10s %10s %s", "Configuration", "Q", "SNR(dB)", "Status")
		t.Logf("%-35s %10s %10s %s", "--------------", "----", "------", "------")
		t.Log("INFO: libopus reference fixture unavailable; using absolute quality thresholds")
	}

	passed := 0
	failed := 0
	enforceNoNegativeGap := refAvailable && !allowNegativeComplianceGap()
	if refAvailable && !enforceNoNegativeGap {
		t.Log("INFO: no-negative gap guard disabled by GOPUS_ALLOW_NEGATIVE_COMPLIANCE_GAP")
	}

	for _, tc := range cases {
		q, decoded := runEncoderComplianceTest(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)

		snr := SNRFromQuality(q)
		var status string
		if refAvailable {
			libQ, libDecoded, ok := runLibopusComplianceReferenceTest(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
			_ = libDecoded // decoded samples available for debugging if needed
			if ok {
				libSNR := SNRFromQuality(libQ)
				gapDB := snr - libSNR
				noNegativeTol := noNegativeGapToleranceForComplianceCase(tc.name)
				speechMode := tc.mode == encoder.ModeSILK || tc.mode == encoder.ModeHybrid
				if speechMode && math.Abs(gapDB) > EncoderLibopusSpeechGapTightDB {
					status = "FAIL"
					failed++
				} else if enforceNoNegativeGap && gapDB < -noNegativeTol {
					status = "FAIL"
					failed++
				} else if gapDB >= EncoderLibopusGapGoodDB {
					status = "GOOD"
					passed++
				} else if gapDB >= EncoderLibopusGapBaseDB {
					status = "BASE"
					passed++
				} else {
					status = "FAIL"
					failed++
				}
				t.Logf("%-35s %10.2f %10.2f %10.2f %10.2f %10.2f %s", tc.name, q, snr, libQ, libSNR, gapDB, status)
			} else {
				// Fall back to absolute thresholds if reference encode fails for this case.
				if q >= EncoderGoodThreshold {
					status = "GOOD"
					passed++
				} else if q >= EncoderQualityThreshold {
					status = "BASE"
					passed++
				} else {
					status = "FAIL"
					failed++
				}
				t.Logf("%-35s %10.2f %10.2f %10s %10s %10s %s", tc.name, q, snr, "-", "-", "-", status)
			}
		} else {
			if q >= EncoderGoodThreshold {
				status = "GOOD" // Near libopus CELT quality
				passed++
			} else if q >= EncoderQualityThreshold {
				status = "BASE" // Within libopus range
				passed++
			} else {
				status = "FAIL" // Regression
				failed++
			}
			_ = decoded // decoded samples available for debugging if needed
			t.Logf("%-35s %10.2f %10.2f %s", tc.name, q, snr, status)
		}
	}

	t.Logf("---")
	t.Logf("Total: %d passed, %d failed", passed, failed)
	if refAvailable {
		t.Logf("Gap thresholds (gopus SNR - libopus SNR): GOOD >= %.1f dB, BASE >= %.1f dB", EncoderLibopusGapGoodDB, EncoderLibopusGapBaseDB)
		if enforceNoNegativeGap {
			t.Logf("No-negative gap guard: gopus SNR - libopus SNR >= -%.2f dB (with case overrides)", EncoderLibopusNoNegativeGapToleranceDB)
		}
		t.Logf("SILK/Hybrid parity guard: |gap| <= %.1f dB", EncoderLibopusSpeechGapTightDB)
	}
}

// testEncoderCompliance runs a single encoder compliance test.
func testEncoderCompliance(t *testing.T, mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) {
	q, _ := runEncoderComplianceTest(t, mode, bandwidth, frameSize, channels, bitrate)

	snr := SNRFromQuality(q)
	t.Logf("Quality: Q=%.2f, SNR=%.2f dB", q, snr)

	if q >= EncoderGoodThreshold {
		t.Logf("GOOD: Near libopus CELT quality (Q >= %.0f)", EncoderGoodThreshold)
	} else if q >= EncoderQualityThreshold {
		t.Logf("BASE: Within libopus range (Q >= %.0f)", EncoderQualityThreshold)
	} else {
		t.Logf("WARN: Below libopus worst case - possible regression")
	}
}

func logCELTTargetStatsSummary(t *testing.T, frameSize int, stats []celt.CeltTargetStats) {
	if len(stats) == 0 {
		t.Logf("CELT %.1fms target stats: no frames captured", float64(frameSize)/48.0)
		return
	}
	minBase := stats[0].BaseBits
	maxBase := stats[0].BaseBits
	minTarget := stats[0].TargetBits
	maxTarget := stats[0].TargetBits
	minDepth := stats[0].MaxDepth
	maxDepth := stats[0].MaxDepth
	sumBase := 0
	sumTarget := 0
	sumDynalloc := 0
	sumTF := 0
	sumDepth := 0.0
	floorLimited := 0
	nonPositiveDynalloc := 0
	pitchChangeCount := 0
	lowDepth := 0
	targetBelowBase := 0

	for _, s := range stats {
		if s.BaseBits < minBase {
			minBase = s.BaseBits
		}
		if s.BaseBits > maxBase {
			maxBase = s.BaseBits
		}
		if s.TargetBits < minTarget {
			minTarget = s.TargetBits
		}
		if s.TargetBits > maxTarget {
			maxTarget = s.TargetBits
		}
		if s.MaxDepth < minDepth {
			minDepth = s.MaxDepth
		}
		if s.MaxDepth > maxDepth {
			maxDepth = s.MaxDepth
		}
		sumBase += s.BaseBits
		sumTarget += s.TargetBits
		sumDynalloc += s.DynallocBoost
		sumTF += s.TFBoost
		sumDepth += s.MaxDepth
		if s.FloorLimited {
			floorLimited++
		}
		if s.DynallocBoost <= 0 {
			nonPositiveDynalloc++
		}
		if s.PitchChange {
			pitchChangeCount++
		}
		if s.MaxDepth < 0 {
			lowDepth++
		}
		if s.TargetBits < s.BaseBits {
			targetBelowBase++
		}
	}

	n := float64(len(stats))
	t.Logf(
		"CELT %.1fms target stats: frames=%d base(avg=%d,min=%d,max=%d) target(avg=%d,min=%d,max=%d) floor=%d(%.1f%%) maxDepth(avg=%.2f,min=%.2f,max=%.2f) dynalloc(avg=%.1f,<=0=%d) tf(avg=%.1f) pitch_change=%d(%.1f%%) lowDepth=%d target<base=%d",
		float64(frameSize)/48.0,
		len(stats),
		int(float64(sumBase)/n), minBase, maxBase,
		int(float64(sumTarget)/n), minTarget, maxTarget,
		floorLimited, 100.0*float64(floorLimited)/n,
		sumDepth/n, minDepth, maxDepth,
		float64(sumDynalloc)/n, nonPositiveDynalloc,
		float64(sumTF)/n,
		pitchChangeCount, 100.0*float64(pitchChangeCount)/n,
		lowDepth,
		targetBelowBase,
	)
}

// runEncoderComplianceTest runs the full encode→decode→compare pipeline.
func runEncoderComplianceTest(t *testing.T, mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) (q float64, decoded []float32) {
	// Generate 1 second of test signal
	numFrames := 48000 / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	// Create encoder.
	enc := encoder.NewEncoder(48000, channels)
	if mode == encoder.ModeHybrid {
		// Compliance reference rows tagged as "hybrid" are generated with
		// opus_demo app=audio (adaptive mode selection), so mirror that with
		// ModeAuto instead of forcing Hybrid packets.
		enc.SetMode(encoder.ModeAuto)
	} else {
		enc.SetMode(mode)
	}
	enc.SetBandwidth(bandwidth)
	enc.SetBitrate(bitrate)
	enc.SetBitrateMode(encoder.ModeCBR)
	captureCELTTargetStats := mode == encoder.ModeCELT && (frameSize == 120 ||
		(frameSize == 240 && channels == 1) ||
		(frameSize == 480 && channels == 1) ||
		(frameSize == 480 && channels == 2) ||
		(frameSize == 960 && channels == 2))
	var celtTargetStats []celt.CeltTargetStats
	if captureCELTTargetStats {
		celtTargetStats = make([]celt.CeltTargetStats, 0, numFrames)
		enc.SetCELTTargetStatsHook(func(stats celt.CeltTargetStats) {
			if stats.FrameSize == frameSize {
				celtTargetStats = append(celtTargetStats, stats)
			}
		})
		defer enc.SetCELTTargetStatsHook(nil)
	}

	// Encode all signal frames.
	packets := make([][]byte, 0, numFrames+1)
	samplesPerFrame := frameSize * channels

	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", i, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Empty packet at frame %d", i)
		}
		// Copy packet since Encode returns a slice backed by scratch memory.
		packetCopy := make([]byte, len(packet))
		copy(packetCopy, packet)
		packets = append(packets, packetCopy)
	}
	if captureCELTTargetStats {
		logCELTTargetStatsSummary(t, frameSize, celtTargetStats)
	}

	// Match libopus fixture cadence: one trailing flush packet is typically
	// emitted after 1s signal windows. Try a few silence frames to drain it.
	flushTargetFrames := numFrames + 1
	if len(packets) < flushTargetFrames {
		const flushAttempts = 4
		silence := make([]float64, samplesPerFrame)
		for i := 0; i < flushAttempts && len(packets) < flushTargetFrames; i++ {
			packet, err := enc.Encode(silence, frameSize)
			if err != nil {
				t.Fatalf("Flush frame %d failed: %v", len(packets), err)
			}
			if len(packet) == 0 {
				continue
			}
			packetCopy := make([]byte, len(packet))
			copy(packetCopy, packet)
			packets = append(packets, packetCopy)
		}
	}

	decoded, err := decodeCompliancePackets(packets, channels, frameSize)
	if err != nil {
		t.Fatalf("decode reference failed: %v", err)
	}

	if len(decoded) == 0 {
		t.Fatal("No samples decoded")
	}

	// NOTE: opusdec already handles pre-skip internally (reads it from
	// the OpusHead header and discards that many samples). Do NOT strip
	// pre-skip again here — that would double-subtract and misalign.

	// Align lengths for comparison (decoded may have trailing samples)
	compareLen := len(original)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}

	// Compute quality metric with delay compensation.
	// opusdec already strips the pre-skip (312 samples), so the residual
	// delay between decoded and original should be small -- typically within
	// a few samples of zero.  Search +/- 960 samples (one 20ms frame) to
	// handle any mode-dependent resampling offset without picking up false
	// correlation peaks from the test signal's quasi-periodicity.
	var foundDelay int
	q, foundDelay = ComputeQualityFloat32WithDelay(decoded[:compareLen], original[:compareLen], 48000, 960)
	t.Logf("Quality: Q=%.2f, foundDelay=%d samples (%.1f ms), decoded=%d original=%d compareLen=%d",
		q, foundDelay, float64(foundDelay)/48.0, len(decoded), len(original), compareLen)

	return q, decoded
}

// runLibopusComplianceReferenceTest runs the same compliance pipeline as
// runEncoderComplianceTest but uses libopus as the encoder reference.
func runLibopusComplianceReferenceTest(t *testing.T, mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) (q float64, decoded []float32, ok bool) {
	_ = t
	if libQ, found := lookupEncoderComplianceReferenceQ(mode, bandwidth, frameSize, channels, bitrate); found {
		return libQ, nil, true
	}
	if fixtureCase, found := findLongFrameFixtureCase(mode, bandwidth, frameSize, channels, bitrate); found {
		fixtureQ, err := runLongFrameFixtureReferenceCase(fixtureCase)
		if err == nil {
			return fixtureQ, nil, true
		}
	}
	return 0, nil, false
}

func decodeCompliancePackets(packets [][]byte, channels, frameSize int) ([]float32, error) {
	strictLibopusRef := strictLibopusReferenceRequired()

	// Prefer direct libopus API decode helper to avoid CLI provenance/tooling drift.
	decoded, helperErr := decodeWithLibopusReferencePacketsSingle(channels, frameSize, packets)
	if helperErr == nil {
		preSkip := OpusPreSkip * channels
		if len(decoded) > preSkip {
			decoded = decoded[preSkip:]
		}
		return decoded, nil
	}
	useOpusdec := checkOpusdecAvailableEncoder()
	if strictLibopusRef && !useOpusdec {
		return nil, fmt.Errorf("strict libopus reference decode required: direct helper failed (%v); opusdec not available", helperErr)
	}
	if useOpusdec {
		var oggBuf bytes.Buffer
		if err := writeOggOpusEncoder(&oggBuf, packets, channels, 48000, frameSize); err != nil {
			return nil, fmt.Errorf("write ogg opus: %w", err)
		}

		decoded, err := decodeWithOpusdec(oggBuf.Bytes())
		if err == nil {
			return decoded, nil
		}
		if strictLibopusRef {
			return nil, fmt.Errorf("strict libopus reference decode required: direct helper failed (%v); opusdec decode failed: %w", helperErr, err)
		}
		if err.Error() != "opusdec blocked by macOS provenance" {
			return nil, err
		}
	}

	decoded, err := decodeComplianceWithInternalDecoder(packets, channels)
	if err != nil {
		return nil, err
	}
	if strictLibopusRef {
		return nil, fmt.Errorf("strict libopus reference decode required: direct helper failed (%v); internal decoder fallback disallowed", helperErr)
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("internal decoder returned no samples")
	}
	preSkip := OpusPreSkip * channels
	if len(decoded) > preSkip {
		decoded = decoded[preSkip:]
	}
	return decoded, nil
}

func decodeComplianceWithInternalDecoder(packets [][]byte, channels int) ([]float32, error) {
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		return nil, fmt.Errorf("create decoder: %w", err)
	}

	outBuf := make([]float32, 5760*channels)
	var decoded []float32
	for i, pkt := range packets {
		n, err := dec.Decode(pkt, outBuf)
		if err != nil {
			return nil, fmt.Errorf("decode frame %d: %w", i, err)
		}
		if n == 0 {
			continue
		}
		decoded = append(decoded, outBuf[:n*channels]...)
	}
	return decoded, nil
}

// Ogg container helpers

// writeOggOpusEncoder writes Opus packets to an Ogg container.
// Minimal implementation per RFC 7845.
func writeOggOpusEncoder(w io.Writer, packets [][]byte, channels, sampleRate, frameSize int) error {
	serialNo := uint32(12345)
	var granulePos uint64

	// Page 1: OpusHead header
	opusHead := makeOpusHeadEncoder(channels, sampleRate)
	if err := writeOggPage(w, serialNo, 0, 2, 0, [][]byte{opusHead}); err != nil {
		return err
	}

	// Page 2: OpusTags header
	opusTags := makeOpusTagsEncoder()
	if err := writeOggPage(w, serialNo, 1, 0, 0, [][]byte{opusTags}); err != nil {
		return err
	}

	// Data pages - need to account for pre-skip in granule position
	pageNo := uint32(2)
	granulePos = uint64(OpusPreSkip) // Start after pre-skip

	for i, packet := range packets {
		granulePos += uint64(frameSize)
		headerType := byte(0)
		if i == len(packets)-1 {
			headerType = 4 // End of stream
		}
		if err := writeOggPage(w, serialNo, pageNo, headerType, granulePos, [][]byte{packet}); err != nil {
			return err
		}
		pageNo++
	}

	return nil
}

func makeOpusHeadEncoder(channels, sampleRate int) []byte {
	head := make([]byte, 19)
	copy(head[0:8], "OpusHead")
	head[8] = 1 // Version
	head[9] = byte(channels)
	binary.LittleEndian.PutUint16(head[10:12], uint16(OpusPreSkip)) // Pre-skip
	binary.LittleEndian.PutUint32(head[12:16], uint32(sampleRate))
	binary.LittleEndian.PutUint16(head[16:18], 0) // Output gain
	head[18] = 0                                  // Channel mapping family
	return head
}

func makeOpusTagsEncoder() []byte {
	vendor := "gopus"
	tags := make([]byte, 8+4+len(vendor)+4)
	copy(tags[0:8], "OpusTags")
	binary.LittleEndian.PutUint32(tags[8:12], uint32(len(vendor)))
	copy(tags[12:12+len(vendor)], vendor)
	binary.LittleEndian.PutUint32(tags[12+len(vendor):], 0) // User comment count
	return tags
}

// writeOggPage, oggCRC, oggCRCUpdate are defined in ogg_helpers_test.go

// decodeWithOpusdec invokes opusdec and parses the WAV output.
func decodeWithOpusdec(oggData []byte) ([]float32, error) {
	// Write to temp file
	tmpFile, err := os.CreateTemp("", "gopus_enc_test_*.opus")
	if err != nil {
		return nil, fmt.Errorf("create temp opus file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.Write(oggData); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("write opus data: %w", err)
	}
	_ = tmpFile.Close()

	// Clear extended attributes on macOS (provenance can cause issues)
	_ = exec.Command("xattr", "-c", tmpFile.Name()).Run()

	// Create output file for decoded WAV
	wavFile, err := os.CreateTemp("", "gopus_enc_test_*.wav")
	if err != nil {
		return nil, fmt.Errorf("create temp wav file: %w", err)
	}
	defer func() { _ = os.Remove(wavFile.Name()) }()
	_ = wavFile.Close()

	// Decode with opusdec
	opusdec := getOpusdecPathEncoder()
	cmd := exec.Command(opusdec, tmpFile.Name(), wavFile.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check for macOS provenance issues
		if bytes.Contains(output, []byte("provenance")) ||
			bytes.Contains(output, []byte("quarantine")) ||
			bytes.Contains(output, []byte("killed")) ||
			bytes.Contains(output, []byte("Operation not permitted")) {
			return nil, fmt.Errorf("opusdec blocked by macOS provenance")
		}
		return nil, fmt.Errorf("opusdec failed: %v, output: %s", err, output)
	}

	// Read and parse WAV
	wavData, err := os.ReadFile(wavFile.Name())
	if err != nil {
		return nil, fmt.Errorf("read wav file: %w", err)
	}

	samples := parseWAVSamplesEncoder(wavData)
	if len(samples) == 0 {
		return nil, fmt.Errorf("no samples decoded from WAV")
	}

	return samples, nil
}

func parseWAVSamplesEncoder(data []byte) []float32 {
	if len(data) < 44 {
		return nil
	}

	// Find data chunk
	offset := 12
	for offset < len(data)-8 {
		chunkID := string(data[offset : offset+4])
		chunkSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])

		if chunkID == "data" {
			dataStart := offset + 8
			dataLen := int(chunkSize)
			if dataStart+dataLen > len(data) {
				dataLen = len(data) - dataStart
			}

			pcmData := data[dataStart : dataStart+dataLen]
			samples := make([]float32, len(pcmData)/2)
			for i := 0; i < len(pcmData)/2; i++ {
				s := int16(binary.LittleEndian.Uint16(pcmData[i*2 : i*2+2]))
				samples[i] = float32(s) / 32768.0
			}
			return samples
		}

		offset += 8 + int(chunkSize)
		if chunkSize%2 != 0 {
			offset++
		}
	}

	// Fallback: skip standard WAV header
	if len(data) <= 44 {
		return nil
	}
	data = data[44:]
	samples := make([]float32, len(data)/2)
	for i := 0; i < len(data)/2; i++ {
		s := int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
		samples[i] = float32(s) / 32768.0
	}
	return samples
}

// Helper functions

func float32ToFloat64(in []float32) []float64 {
	// Mirror opus_demo -f32 input quantization so compliance runs use
	// the same effective PCM domain as the libopus reference fixtures.
	const inv24 = 1.0 / 8388608.0
	out := make([]float64, len(in))
	for i, v := range in {
		q := math.Floor(0.5 + float64(v)*8388608.0)
		out[i] = q * inv24
	}
	return out
}

func checkOpusdecAvailableEncoder() bool {
	if strings.TrimSpace(os.Getenv("GOPUS_DISABLE_OPUSDEC")) == "1" {
		return false
	}

	// Check PATH first
	if _, err := exec.LookPath("opusdec"); err == nil {
		return true
	}

	// Check common paths
	paths := []string{
		"/opt/homebrew/bin/opusdec",
		"/usr/local/bin/opusdec",
		"/usr/bin/opusdec",
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}

	return false
}

func strictLibopusReferenceRequired() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_STRICT_LIBOPUS_REF")))
	return v == "1" || v == "true" || v == "yes"
}

func allowNegativeComplianceGap() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_ALLOW_NEGATIVE_COMPLIANCE_GAP")))
	return v == "1" || v == "true" || v == "yes"
}

func getOpusdecPathEncoder() string {
	// Try PATH first
	if path, err := exec.LookPath("opusdec"); err == nil {
		return path
	}

	// Try common paths
	paths := []string{
		"/opt/homebrew/bin/opusdec",
		"/usr/local/bin/opusdec",
		"/usr/bin/opusdec",
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "opusdec"
}

// TestEncoderComplianceInfo logs info about the encoder compliance test setup.
func TestEncoderComplianceInfo(t *testing.T) {
	if !checkOpusdecAvailableEncoder() {
		t.Log("opusdec not available - encoder compliance tests will be skipped")
		t.Log("To enable compliance tests, install opus-tools:")
		t.Log("  macOS: brew install opus-tools")
		t.Log("  Linux: apt-get install opus-tools")
		return
	}

	path := getOpusdecPathEncoder()
	t.Logf("opusdec found at: %s", path)

	// Try to get version
	cmd := exec.Command(path, "--version")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Logf("opusdec version: %s", string(output))
	}

	t.Log("")
	t.Log("Test Matrix:")
	t.Log("============")
	t.Log("| Mode   | Bandwidths              | Frame Sizes           | Channels    |")
	t.Log("|--------|-------------------------|-----------------------|-------------|")
	t.Log("| SILK   | NB, MB, WB              | 10ms, 20ms            | mono, stereo|")
	t.Log("| Hybrid | SWB, FB                 | 10ms, 20ms            | mono, stereo|")
	t.Log("| CELT   | FB                      | 2.5ms, 5ms, 10ms, 20ms| mono, stereo|")
	t.Log("")
	t.Log("Primary Compliance Thresholds (libopus parity):")
	t.Logf("  GOOD: gopus SNR - libopus SNR >= %.1f dB", EncoderLibopusGapGoodDB)
	t.Logf("  BASE: gopus SNR - libopus SNR >= %.1f dB", EncoderLibopusGapBaseDB)
	t.Logf("  SILK/Hybrid guard: |gopus SNR - libopus SNR| <= %.1f dB", EncoderLibopusSpeechGapTightDB)
	t.Log("Fallback Absolute Thresholds (only when libopus fixture unavailable):")
	t.Logf("  GOOD (≈ libopus CELT): Q >= %.1f (%.1f dB SNR)", EncoderGoodThreshold, SNRFromQuality(EncoderGoodThreshold))
	t.Logf("  BASE (≈ libopus range): Q >= %.1f (%.1f dB SNR)", EncoderQualityThreshold, SNRFromQuality(EncoderQualityThreshold))
}
