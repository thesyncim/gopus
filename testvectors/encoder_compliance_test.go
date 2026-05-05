// Package testvectors provides encoder compliance testing.
// This file validates gopus encoder output by encoding raw PCM audio,
// decoding with libopus, and comparing decoded audio to the original input
// using the pinned libopus opus_compare quality metric.
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
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// Quality thresholds for encoder compliance.
//
// Primary compliance target: relative parity against libopus reference packets
// measured with the real opus_compare metric. Gap is reported as
// (gopus opus_compare Q - libopus opus_compare Q), so larger is better.
//
// Absolute thresholds are fallback-only and apply when no libopus reference
// packets are available for a given case.
const (
	// EncoderQualityThreshold is the absolute regression guard when no libopus
	// packet reference is available.
	EncoderQualityThreshold = 0.0

	// EncoderGoodThreshold indicates comfortably positive opus_compare quality.
	EncoderGoodThreshold = 20.0

	// Pre-skip samples as defined in Ogg Opus header
	OpusPreSkip = 312

	// Relative quality targets versus libopus reference (when available).
	// Gap is reported as (gopus Q - libopus Q).
	EncoderLibopusGapGoodQ = -0.5
	EncoderLibopusGapBaseQ = -2.0

	// For SILK/Hybrid, we still guard against suspicious overshoot or undershoot
	// relative to libopus, but with the real perceptual metric rather than dB.
	EncoderLibopusSpeechGapTightQ = 6.0
)

var encoderComplianceLogOnce sync.Once

func logEncoderComplianceStatus(t *testing.T) {
	encoderComplianceLogOnce.Do(func() {
		t.Log("TARGET: Encoder compliance is parity-first against libopus packet references measured with opus_compare.")
		t.Logf("TARGET: Gap thresholds (gopus Q - libopus Q): GOOD >= %.1f, BASE >= %.1f", EncoderLibopusGapGoodQ, EncoderLibopusGapBaseQ)
		t.Logf("TARGET: Precision floors: per-profile libopus Q-gap floors with %.2f Q tolerance", encoderLibopusGapMeasurementToleranceQ)
		t.Logf("TARGET: SILK/Hybrid parity guard: |gap| <= %.1f Q", EncoderLibopusSpeechGapTightQ)
		t.Log("FALLBACK: Absolute opus_compare Q thresholds apply only when libopus packet references are unavailable.")
	})
}

type encoderComplianceSummaryCase struct {
	name      string
	mode      encoder.Mode
	bandwidth types.Bandwidth
	frameSize int
	channels  int
	bitrate   int
}

type encoderComplianceCaseKey struct {
	mode      encoder.Mode
	bandwidth types.Bandwidth
	frameSize int
	channels  int
	bitrate   int
}

type encoderComplianceRunResult struct {
	q           float64
	decoded     []float32
	foundDelay  int
	decodedLen  int
	originalLen int
	compareLen  int
}

type encoderComplianceRunEntry struct {
	once   sync.Once
	result encoderComplianceRunResult
	err    error
}

type libopusComplianceReferenceResult struct {
	q       float64
	ok      bool
	warning string
}

type libopusComplianceReferenceEntry struct {
	once   sync.Once
	result libopusComplianceReferenceResult
}

var (
	encoderComplianceRunCache       sync.Map
	libopusComplianceReferenceCache sync.Map
)

func encoderComplianceKey(mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) encoderComplianceCaseKey {
	return encoderComplianceCaseKey{
		mode:      mode,
		bandwidth: bandwidth,
		frameSize: frameSize,
		channels:  channels,
		bitrate:   bitrate,
	}
}

func encoderComplianceRunCacheEntry(key encoderComplianceCaseKey) *encoderComplianceRunEntry {
	entry := &encoderComplianceRunEntry{}
	actual, _ := encoderComplianceRunCache.LoadOrStore(key, entry)
	return actual.(*encoderComplianceRunEntry)
}

func libopusComplianceReferenceCacheEntry(key encoderComplianceCaseKey) *libopusComplianceReferenceEntry {
	entry := &libopusComplianceReferenceEntry{}
	actual, _ := libopusComplianceReferenceCache.LoadOrStore(key, entry)
	return actual.(*libopusComplianceReferenceEntry)
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

	type caseResult struct {
		q      float64
		libQ   float64
		gapQ   float64
		status string
		refOK  bool
		passed bool
	}
	results := make([]caseResult, len(cases))

	// Run cases as parallel subtests; the parent test blocks on t.Run until
	// every subtest completes, so the summary below sees final results.
	t.Run("cases", func(t *testing.T) {
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				q, _ := runEncoderComplianceTest(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)

				res := caseResult{q: q}
				if refAvailable {
					libQ, _, ok := runLibopusComplianceReferenceTest(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
					res.refOK = ok
					if ok {
						res.libQ = libQ
						res.gapQ = q - libQ
						status, floor := encoderComplianceReferenceStatusForCase(tc.name, res.gapQ)
						res.status = status
						if status == "FAIL" {
							t.Errorf("precision floor miss for %s: gap=%.2f Q floor=%.2f Q tol=%.2f Q", tc.name, res.gapQ, floor, encoderLibopusGapMeasurementToleranceQ)
						} else {
							res.passed = true
						}
					}
				}
				if !res.refOK {
					switch {
					case q >= EncoderGoodThreshold:
						res.status = "GOOD"
						res.passed = true
					case q >= EncoderQualityThreshold:
						res.status = "BASE"
						res.passed = true
					default:
						res.status = "FAIL"
						t.Errorf("absolute quality floor miss for %s: Q=%.2f", tc.name, q)
					}
				}
				results[i] = res
			})
		}
	})

	if refAvailable {
		t.Log("Encoder Compliance Summary (Target: libopus reference)")
		t.Log("======================================================")
		t.Logf("%-35s %10s %10s %10s %s", "Configuration", "Q", "LibQ", "GapQ", "Status")
		t.Logf("%-35s %10s %10s %10s %s", "--------------", "----", "----", "----", "------")
	} else {
		t.Log("Encoder Compliance Summary")
		t.Log("===========================")
		t.Logf("%-35s %10s %s", "Configuration", "Q", "Status")
		t.Logf("%-35s %10s %s", "--------------", "----", "------")
		t.Log("INFO: libopus reference fixture unavailable; using absolute quality thresholds")
	}

	passed, failed := 0, 0
	for i, tc := range cases {
		res := results[i]
		if res.passed {
			passed++
		} else {
			failed++
		}
		if refAvailable {
			if res.refOK {
				t.Logf("%-35s %10.2f %10.2f %10.2f %s", tc.name, res.q, res.libQ, res.gapQ, res.status)
			} else {
				t.Logf("%-35s %10.2f %10s %10s %s", tc.name, res.q, "-", "-", res.status)
			}
		} else {
			t.Logf("%-35s %10.2f %s", tc.name, res.q, res.status)
		}
	}

	t.Logf("---")
	t.Logf("Total: %d passed, %d failed", passed, failed)
	if refAvailable {
		t.Logf("Gap thresholds (gopus Q - libopus Q): GOOD >= %.1f, BASE >= %.1f", EncoderLibopusGapGoodQ, EncoderLibopusGapBaseQ)
		t.Logf("Precision floor guard: per-profile floors with %.2f Q measurement tolerance", encoderLibopusGapMeasurementToleranceQ)
	}
}

// testEncoderCompliance runs a single encoder compliance test.
func testEncoderCompliance(t *testing.T, mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) {
	q, _ := runEncoderComplianceTest(t, mode, bandwidth, frameSize, channels, bitrate)

	t.Logf("Quality: Q=%.2f", q)

	if q >= EncoderGoodThreshold {
		t.Logf("GOOD: Strong opus_compare quality (Q >= %.0f)", EncoderGoodThreshold)
	} else if q >= EncoderQualityThreshold {
		t.Logf("BASE: Acceptable opus_compare quality (Q >= %.0f)", EncoderQualityThreshold)
	} else {
		t.Logf("WARN: Below fallback opus_compare floor - possible regression")
	}
}

func computeEncoderComplianceResult(mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) (encoderComplianceRunResult, error) {
	var result encoderComplianceRunResult

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
	// CELT compliance/reference rows are generated with opus_demo
	// `-e restricted-celt`, which disables the top-level Opus delay buffer.
	// Mirror that application profile so packet/quality comparisons stay
	// aligned with the libopus fixture we are judging against.
	enc.SetLowDelay(mode == encoder.ModeCELT)
	enc.SetBandwidth(bandwidth)
	enc.SetBitrate(bitrate)
	enc.SetBitrateMode(encoder.ModeCBR)

	// Encode all signal frames.
	packets := make([][]byte, 0, numFrames+1)
	samplesPerFrame := frameSize * channels

	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64OpusDemoF32(original[start:end])

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			return result, fmt.Errorf("encode frame %d failed: %w", i, err)
		}
		if len(packet) == 0 {
			return result, fmt.Errorf("empty packet at frame %d", i)
		}
		// Copy packet since Encode returns a slice backed by scratch memory.
		packetCopy := make([]byte, len(packet))
		copy(packetCopy, packet)
		packets = append(packets, packetCopy)
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
				return result, fmt.Errorf("flush frame %d failed: %w", len(packets), err)
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
		return result, fmt.Errorf("decode reference failed: %w", err)
	}

	if len(decoded) == 0 {
		return result, fmt.Errorf("no samples decoded")
	}

	// NOTE: opusdec already handles pre-skip internally (reads it from
	// the OpusHead header and discards that many samples). Do NOT strip
	// pre-skip again here — that would double-subtract and misalign.

	// Align lengths for comparison (decoded may have trailing samples)
	compareLen := len(original)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}

	// Search up to one packet duration of residual delay after pre-skip
	// trimming. Short CELT cases can legitimately land beyond +/-32 samples, but
	// we still keep the window local to avoid distant periodic alias matches.
	result.q, result.foundDelay, err = ComputeOpusCompareQualityFloat32WithDelay(
		decoded[:compareLen],
		original[:compareLen],
		48000,
		channels,
		qualityDelaySearchWindow(frameSize),
	)
	if err != nil {
		return result, fmt.Errorf("compute opus_compare quality: %w", err)
	}
	result.decoded = decoded
	result.decodedLen = len(decoded)
	result.originalLen = len(original)
	result.compareLen = compareLen
	return result, nil
}

// runEncoderComplianceTest runs the full encode→decode→compare pipeline.
func runEncoderComplianceTest(t *testing.T, mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) (q float64, decoded []float32) {
	key := encoderComplianceKey(mode, bandwidth, frameSize, channels, bitrate)
	entry := encoderComplianceRunCacheEntry(key)
	entry.once.Do(func() {
		entry.result, entry.err = computeEncoderComplianceResult(mode, bandwidth, frameSize, channels, bitrate)
	})
	if entry.err != nil {
		t.Fatal(entry.err)
	}
	t.Logf("Quality: Q=%.2f, foundDelay=%d samples (%.1f ms), decoded=%d original=%d compareLen=%d",
		entry.result.q,
		entry.result.foundDelay,
		float64(entry.result.foundDelay)/48.0,
		entry.result.decodedLen,
		entry.result.originalLen,
		entry.result.compareLen,
	)
	return entry.result.q, entry.result.decoded
}

// runLibopusComplianceReferenceTest runs the same compliance pipeline as
// runEncoderComplianceTest but uses libopus as the encoder reference.
func runLibopusComplianceReferenceTest(t *testing.T, mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) (q float64, decoded []float32, ok bool) {
	key := encoderComplianceKey(mode, bandwidth, frameSize, channels, bitrate)
	entry := libopusComplianceReferenceCacheEntry(key)
	entry.once.Do(func() {
		if fixtureCase, found := findEncoderVariantsFixtureCase(mode, bandwidth, frameSize, channels, bitrate, defaultEncoderSignalVariant); found {
			packets, _, err := decodeEncoderVariantsFixturePackets(fixtureCase)
			if err == nil {
				numFrames := 48000 / frameSize
				totalSamples := numFrames * frameSize * channels
				original := generateEncoderTestSignal(totalSamples, channels)
				q, err := computeOpusCompareQualityFromPackets(packets, original, channels, frameSize)
				if err == nil {
					entry.result.q = q
					entry.result.ok = true
					return
				}
				entry.result.warning = fmt.Sprintf(
					"live libopus variants quality unavailable for %s/%s/%d/%d/%d: %v",
					fixtureModeName(mode),
					fixtureBandwidthName(bandwidth),
					frameSize,
					channels,
					bitrate,
					err,
				)
			}
		}
		if fixtureCase, found := findEncoderCompliancePacketsFixtureCase(mode, bandwidth, frameSize, channels, bitrate); found {
			packets, _, err := decodeEncoderPacketsFixturePackets(fixtureCase)
			if err == nil {
				numFrames := 48000 / frameSize
				totalSamples := numFrames * frameSize * channels
				original := generateEncoderTestSignal(totalSamples, channels)
				q, err := computeOpusCompareQualityFromPackets(packets, original, channels, frameSize)
				if err == nil {
					entry.result.q = q
					entry.result.ok = true
					return
				}
				entry.result.warning = fmt.Sprintf(
					"live libopus packet quality unavailable for %s/%s/%d/%d/%d: %v",
					fixtureModeName(mode),
					fixtureBandwidthName(bandwidth),
					frameSize,
					channels,
					bitrate,
					err,
				)
			}
		}
	})
	if entry.result.warning != "" {
		t.Log(entry.result.warning)
	}
	return entry.result.q, nil, entry.result.ok
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
	t.Logf("  GOOD: gopus Q - libopus Q >= %.1f", EncoderLibopusGapGoodQ)
	t.Logf("  BASE: pass per-profile precision floor (current nominal target >= %.1f)", EncoderLibopusGapBaseQ)
	t.Logf("  Precision floors: per-profile libopus gap floors with %.2f Q tolerance", encoderLibopusGapMeasurementToleranceQ)
	t.Log("Fallback Absolute Thresholds (only when libopus fixture unavailable):")
	t.Logf("  GOOD: Q >= %.1f", EncoderGoodThreshold)
	t.Logf("  BASE: Q >= %.1f", EncoderQualityThreshold)
}
