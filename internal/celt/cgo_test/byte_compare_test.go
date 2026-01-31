// Package cgo provides byte-level comparison tests between gopus and libopus encoders.
// This test finds exactly where the encoder bitstreams diverge.
package cgo

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

// ByteCompareConfig defines a configuration for byte comparison tests.
type ByteCompareConfig struct {
	Name        string
	Channels    int
	FrameSize   int
	Bitrate     int
	Bandwidth   int
	Application int
	Complexity  int
	SignalType  int
	TestSignal  string // "sine", "silence", "dc", "noise"
	Frequency   float64
	Amplitude   float64
}

// ByteCompareResult holds the result of a byte comparison.
type ByteCompareResult struct {
	Config          ByteCompareConfig
	GopusBytes      []byte
	LibopusBytes    []byte
	FirstDivergence int // -1 if match
	MatchCount      int
	TotalBytes      int
	GopusFinalRange uint32
	LibopusFinalRng uint32
	Context         string // Description of what was being encoded at divergence
}

// TestByteCompare_CELTMono20ms tests CELT mono 20ms encoding (simplest case).
func TestByteCompare_CELTMono20ms(t *testing.T) {
	cfg := ByteCompareConfig{
		Name:        "CELT-Mono-20ms-64kbps",
		Channels:    1,
		FrameSize:   960,
		Bitrate:     64000,
		Bandwidth:   OpusBandwidthFullband,
		Application: OpusApplicationAudio,
		Complexity:  10,
		SignalType:  OpusSignalMusic,
		TestSignal:  "sine",
		Frequency:   440.0,
		Amplitude:   0.5,
	}

	result := runByteCompareBC(t, cfg)
	logByteCompareResultBC(t, result)
}

// TestByteCompare_CELTStereo20ms tests CELT stereo 20ms encoding.
func TestByteCompare_CELTStereo20ms(t *testing.T) {
	cfg := ByteCompareConfig{
		Name:        "CELT-Stereo-20ms-128kbps",
		Channels:    2,
		FrameSize:   960,
		Bitrate:     128000,
		Bandwidth:   OpusBandwidthFullband,
		Application: OpusApplicationAudio,
		Complexity:  10,
		SignalType:  OpusSignalMusic,
		TestSignal:  "sine",
		Frequency:   440.0,
		Amplitude:   0.5,
	}

	result := runByteCompareBC(t, cfg)
	logByteCompareResultBC(t, result)
}

// TestByteCompare_Silence tests silence encoding.
func TestByteCompare_Silence(t *testing.T) {
	cfg := ByteCompareConfig{
		Name:        "CELT-Mono-20ms-Silence",
		Channels:    1,
		FrameSize:   960,
		Bitrate:     64000,
		Bandwidth:   OpusBandwidthFullband,
		Application: OpusApplicationAudio,
		Complexity:  10,
		SignalType:  OpusSignalMusic,
		TestSignal:  "silence",
		Frequency:   0,
		Amplitude:   0,
	}

	result := runByteCompareBC(t, cfg)
	logByteCompareResultBC(t, result)
}

// TestByteCompare_DC tests DC (constant) signal encoding.
func TestByteCompare_DC(t *testing.T) {
	cfg := ByteCompareConfig{
		Name:        "CELT-Mono-20ms-DC",
		Channels:    1,
		FrameSize:   960,
		Bitrate:     64000,
		Bandwidth:   OpusBandwidthFullband,
		Application: OpusApplicationAudio,
		Complexity:  10,
		SignalType:  OpusSignalMusic,
		TestSignal:  "dc",
		Frequency:   0,
		Amplitude:   0.3,
	}

	result := runByteCompareBC(t, cfg)
	logByteCompareResultBC(t, result)
}

// TestByteCompare_AllConfigs runs byte comparison for multiple configurations.
func TestByteCompare_AllConfigs(t *testing.T) {
	configs := []ByteCompareConfig{
		// CELT mono
		{Name: "CELT-Mono-20ms-64k-sine", Channels: 1, FrameSize: 960, Bitrate: 64000, Bandwidth: OpusBandwidthFullband, Application: OpusApplicationAudio, Complexity: 10, TestSignal: "sine", Frequency: 440, Amplitude: 0.5},
		{Name: "CELT-Mono-20ms-32k-sine", Channels: 1, FrameSize: 960, Bitrate: 32000, Bandwidth: OpusBandwidthFullband, Application: OpusApplicationAudio, Complexity: 10, TestSignal: "sine", Frequency: 440, Amplitude: 0.5},
		{Name: "CELT-Mono-10ms-64k-sine", Channels: 1, FrameSize: 480, Bitrate: 64000, Bandwidth: OpusBandwidthFullband, Application: OpusApplicationAudio, Complexity: 10, TestSignal: "sine", Frequency: 440, Amplitude: 0.5},

		// CELT stereo
		{Name: "CELT-Stereo-20ms-128k-sine", Channels: 2, FrameSize: 960, Bitrate: 128000, Bandwidth: OpusBandwidthFullband, Application: OpusApplicationAudio, Complexity: 10, TestSignal: "sine", Frequency: 440, Amplitude: 0.5},
		{Name: "CELT-Stereo-20ms-64k-sine", Channels: 2, FrameSize: 960, Bitrate: 64000, Bandwidth: OpusBandwidthFullband, Application: OpusApplicationAudio, Complexity: 10, TestSignal: "sine", Frequency: 440, Amplitude: 0.5},

		// Different signals
		{Name: "CELT-Mono-20ms-64k-silence", Channels: 1, FrameSize: 960, Bitrate: 64000, Bandwidth: OpusBandwidthFullband, Application: OpusApplicationAudio, Complexity: 10, TestSignal: "silence", Frequency: 0, Amplitude: 0},
		{Name: "CELT-Mono-20ms-64k-dc", Channels: 1, FrameSize: 960, Bitrate: 64000, Bandwidth: OpusBandwidthFullband, Application: OpusApplicationAudio, Complexity: 10, TestSignal: "dc", Frequency: 0, Amplitude: 0.3},
		{Name: "CELT-Mono-20ms-64k-noise", Channels: 1, FrameSize: 960, Bitrate: 64000, Bandwidth: OpusBandwidthFullband, Application: OpusApplicationAudio, Complexity: 10, TestSignal: "noise", Frequency: 0, Amplitude: 0.3},

		// Different complexities
		{Name: "CELT-Mono-20ms-64k-c0", Channels: 1, FrameSize: 960, Bitrate: 64000, Bandwidth: OpusBandwidthFullband, Application: OpusApplicationAudio, Complexity: 0, TestSignal: "sine", Frequency: 440, Amplitude: 0.5},
		{Name: "CELT-Mono-20ms-64k-c5", Channels: 1, FrameSize: 960, Bitrate: 64000, Bandwidth: OpusBandwidthFullband, Application: OpusApplicationAudio, Complexity: 5, TestSignal: "sine", Frequency: 440, Amplitude: 0.5},
	}

	t.Log("")
	t.Log("Byte Comparison Summary")
	t.Log(strings.Repeat("=", 90))
	t.Logf("%-40s | %8s | %8s | %10s | %s", "Config", "Matches", "Total", "Divergence", "Status")
	t.Log(strings.Repeat("-", 90))

	for _, cfg := range configs {
		result := runByteCompareBC(t, cfg)

		var status string
		var divPos string
		if result.FirstDivergence < 0 {
			status = "EXACT MATCH"
			divPos = "-"
		} else {
			status = "DIVERGE"
			divPos = fmt.Sprintf("@byte %d", result.FirstDivergence)
		}

		t.Logf("%-40s | %8d | %8d | %10s | %s",
			cfg.Name, result.MatchCount, result.TotalBytes, divPos, status)
	}

	t.Log(strings.Repeat("=", 90))
}

// TestByteCompare_DetailedTrace runs a detailed trace for the simplest config.
func TestByteCompare_DetailedTrace(t *testing.T) {
	cfg := ByteCompareConfig{
		Name:        "CELT-Mono-20ms-Trace",
		Channels:    1,
		FrameSize:   960,
		Bitrate:     64000,
		Bandwidth:   OpusBandwidthFullband,
		Application: OpusApplicationAudio,
		Complexity:  10,
		SignalType:  OpusSignalMusic,
		TestSignal:  "sine",
		Frequency:   440.0,
		Amplitude:   0.5,
	}

	result := runByteCompareWithTraceBC(t, cfg)
	logDetailedByteComparisonBC(t, result)
}

// runByteCompareBC executes a byte comparison test.
func runByteCompareBC(t *testing.T, cfg ByteCompareConfig) ByteCompareResult {
	t.Helper()

	result := ByteCompareResult{
		Config:          cfg,
		FirstDivergence: -1,
	}

	// Generate test signal
	pcm := generateTestSignalBC(cfg)

	// Encode with gopus
	gopusBytes, gopusFinalRng, err := encodeWithGopusBC(pcm, cfg)
	if err != nil {
		t.Logf("gopus encode error: %v", err)
		return result
	}
	result.GopusBytes = gopusBytes
	result.GopusFinalRange = gopusFinalRng

	// Encode with libopus
	libopusBytes, libopusFinalRng, err := encodeWithLibopusBC(pcm, cfg)
	if err != nil {
		t.Logf("libopus encode error: %v", err)
		return result
	}
	result.LibopusBytes = libopusBytes
	result.LibopusFinalRng = libopusFinalRng

	// Compare bytes
	result.TotalBytes = maxIntBC(len(gopusBytes), len(libopusBytes))
	result.MatchCount, result.FirstDivergence = compareBytesBC(gopusBytes, libopusBytes)

	return result
}

// runByteCompareWithTraceBC executes byte comparison with detailed tracing.
func runByteCompareWithTraceBC(t *testing.T, cfg ByteCompareConfig) ByteCompareResult {
	result := runByteCompareBC(t, cfg)

	// If there's a divergence, try to identify context
	if result.FirstDivergence >= 0 {
		result.Context = identifyDivergenceContextBC(result.FirstDivergence, cfg)
	}

	return result
}

// generateTestSignalBC creates a test signal based on configuration.
func generateTestSignalBC(cfg ByteCompareConfig) []float32 {
	totalSamples := cfg.FrameSize * cfg.Channels
	pcm := make([]float32, totalSamples)

	switch cfg.TestSignal {
	case "silence":
		// All zeros
	case "dc":
		for i := range pcm {
			pcm[i] = float32(cfg.Amplitude)
		}
	case "sine":
		sampleRate := 48000.0
		for i := 0; i < cfg.FrameSize; i++ {
			sample := cfg.Amplitude * math.Sin(2*math.Pi*cfg.Frequency*float64(i)/sampleRate)
			if cfg.Channels == 1 {
				pcm[i] = float32(sample)
			} else {
				pcm[i*2] = float32(sample)
				// Slightly different frequency on right channel
				sampleR := cfg.Amplitude * math.Sin(2*math.Pi*cfg.Frequency*1.01*float64(i)/sampleRate)
				pcm[i*2+1] = float32(sampleR)
			}
		}
	case "noise":
		// Simple PRNG for deterministic noise
		seed := uint32(12345)
		for i := range pcm {
			seed = seed*1664525 + 1013904223
			pcm[i] = float32(cfg.Amplitude) * (float32(seed)/float32(1<<32)*2 - 1)
		}
	default:
		// Default to sine
		sampleRate := 48000.0
		for i := 0; i < cfg.FrameSize; i++ {
			sample := 0.5 * math.Sin(2*math.Pi*440*float64(i)/sampleRate)
			if cfg.Channels == 1 {
				pcm[i] = float32(sample)
			} else {
				pcm[i*2] = float32(sample)
				pcm[i*2+1] = float32(sample)
			}
		}
	}

	return pcm
}

// encodeWithGopusBC encodes using the gopus encoder.
func encodeWithGopusBC(pcm []float32, cfg ByteCompareConfig) ([]byte, uint32, error) {
	// Create gopus encoder
	enc, err := gopus.NewEncoder(48000, cfg.Channels, gopus.ApplicationAudio)
	if err != nil {
		return nil, 0, err
	}

	_ = enc.SetBitrate(cfg.Bitrate)
	enc.SetFrameSize(cfg.FrameSize)

	// Encode using float32 API
	data := make([]byte, 4000)
	n, err := enc.Encode(pcm, data)
	if err != nil {
		return nil, 0, err
	}

	// Get final range from encoder state
	finalRange := enc.FinalRange()
	return data[:n], finalRange, nil
}

// encodeWithLibopusBC encodes using the libopus encoder.
func encodeWithLibopusBC(pcm []float32, cfg ByteCompareConfig) ([]byte, uint32, error) {
	enc, err := NewLibopusEncoder(48000, cfg.Channels, cfg.Application)
	if err != nil || enc == nil {
		return nil, 0, fmt.Errorf("failed to create libopus encoder")
	}
	defer enc.Destroy()

	enc.SetBitrate(cfg.Bitrate)
	enc.SetComplexity(cfg.Complexity)
	enc.SetBandwidth(cfg.Bandwidth)
	enc.SetVBR(true)
	enc.SetSignal(cfg.SignalType)

	data, n := enc.EncodeFloat(pcm, cfg.FrameSize)
	if n < 0 {
		return nil, 0, fmt.Errorf("libopus encode failed: %d", n)
	}

	finalRange := enc.GetFinalRange()
	return data, finalRange, nil
}

// compareBytesBC compares two byte slices and returns match count and first divergence position.
func compareBytesBC(a, b []byte) (matchCount, firstDivergence int) {
	firstDivergence = -1
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}

	for i := 0; i < minLen; i++ {
		if a[i] == b[i] {
			matchCount++
		} else if firstDivergence < 0 {
			firstDivergence = i
		}
	}

	// Check for length mismatch
	if len(a) != len(b) && firstDivergence < 0 {
		firstDivergence = minLen
	}

	return matchCount, firstDivergence
}

// identifyDivergenceContextBC tries to identify what was being encoded at the divergence point.
func identifyDivergenceContextBC(bytePos int, cfg ByteCompareConfig) string {
	// Estimate based on byte position and frame structure
	// This is approximate - actual context requires detailed tracing

	if bytePos == 0 {
		return "TOC byte"
	}
	if bytePos == 1 {
		return "First payload byte (silence/postfilter flags)"
	}

	// Approximate structure for 20ms CELT frame at 64kbps (~160 bytes):
	// - Byte 0: TOC
	// - Bytes 1-2: Flags (silence, postfilter, transient, intra)
	// - Bytes 2-10: Coarse energy (~60-80 bits)
	// - Bytes 10-15: TF, spread, dynalloc, trim
	// - Bytes 15-30: Fine energy
	// - Bytes 30+: PVQ band quantization

	switch {
	case bytePos < 3:
		return "Frame flags region (silence/postfilter/transient/intra)"
	case bytePos < 10:
		return "Coarse energy encoding"
	case bytePos < 15:
		return "TF/spread/dynalloc/trim region"
	case bytePos < 30:
		return "Fine energy encoding"
	default:
		// Estimate which band
		band := (bytePos - 30) / 8 // Very rough estimate
		return fmt.Sprintf("PVQ band quantization (approx band %d)", band)
	}
}

// logByteCompareResultBC logs the result of a byte comparison.
func logByteCompareResultBC(t *testing.T, result ByteCompareResult) {
	t.Logf("\n=== %s ===", result.Config.Name)
	t.Logf("gopus:   %d bytes, final_range=0x%08X", len(result.GopusBytes), result.GopusFinalRange)
	t.Logf("libopus: %d bytes, final_range=0x%08X", len(result.LibopusBytes), result.LibopusFinalRng)

	if result.FirstDivergence < 0 {
		t.Log("Result: EXACT MATCH")
	} else {
		t.Logf("Result: DIVERGE at byte %d", result.FirstDivergence)
		t.Logf("Context: %s", result.Context)
		t.Logf("Matching bytes: %d/%d", result.MatchCount, result.TotalBytes)
	}
}

// logDetailedByteComparisonBC logs a detailed byte-by-byte comparison.
func logDetailedByteComparisonBC(t *testing.T, result ByteCompareResult) {
	t.Log("")
	t.Logf("=== Detailed Byte Comparison: %s ===", result.Config.Name)
	t.Log("")

	// Header
	t.Logf("%-6s | %-10s | %-10s | %s", "Byte", "gopus", "libopus", "Status")
	t.Log(strings.Repeat("-", 50))

	maxLen := maxIntBC(len(result.GopusBytes), len(result.LibopusBytes))
	showContextAt := -1

	for i := 0; i < maxLen; i++ {
		var gVal, lVal string
		var status string

		if i < len(result.GopusBytes) {
			gVal = fmt.Sprintf("0x%02X", result.GopusBytes[i])
		} else {
			gVal = "---"
		}

		if i < len(result.LibopusBytes) {
			lVal = fmt.Sprintf("0x%02X", result.LibopusBytes[i])
		} else {
			lVal = "---"
		}

		if i < len(result.GopusBytes) && i < len(result.LibopusBytes) {
			if result.GopusBytes[i] == result.LibopusBytes[i] {
				status = "MATCH"
			} else {
				if showContextAt < 0 {
					showContextAt = i
					status = "DIVERGE <-- First mismatch!"
				} else {
					status = "DIVERGE"
				}
			}
		} else {
			status = "LENGTH"
		}

		t.Logf("%-6d | %-10s | %-10s | %s", i, gVal, lVal, status)

		// Add context after first divergence
		if i == showContextAt {
			ctx := identifyDivergenceContextBC(i, result.Config)
			t.Logf("       | Context: %s", ctx)
		}

		// Limit output for very long packets
		if i > 50 && i > showContextAt+10 && showContextAt >= 0 {
			t.Logf("... (%d more bytes)", maxLen-i-1)
			break
		}
	}

	t.Log(strings.Repeat("-", 50))
	t.Logf("Total: %d bytes gopus, %d bytes libopus", len(result.GopusBytes), len(result.LibopusBytes))
	if result.FirstDivergence >= 0 {
		t.Logf("First divergence at byte %d", result.FirstDivergence)
	} else {
		t.Log("Result: EXACT MATCH")
	}
}

// TestByteCompare_InternalCELTEncoder tests the internal CELT encoder directly.
func TestByteCompare_InternalCELTEncoder(t *testing.T) {
	frameSize := 960
	channels := 1
	freq := 440.0
	amp := 0.5

	// Generate test signal
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = amp * math.Sin(2.0*math.Pi*freq*float64(i)/48000.0)
	}

	// Encode with gopus CELT encoder directly
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	encoded, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("CELT encode failed: %v", err)
	}

	t.Logf("CELT encoded: %d bytes", len(encoded))
	t.Logf("First 32 bytes: %02x", encoded[:minIntBC(32, len(encoded))])

	// Create packet with TOC for libopus decoding
	// Config 31 = CELT FB 20ms, mono
	toc := byte((31 << 3) | 0)
	packet := append([]byte{toc}, encoded...)

	// Decode with libopus
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Logf("libopus decode returned %d (possibly invalid packet)", libSamples)
	} else {
		// Check decoded signal energy
		var libEnergy float64
		for i := 0; i < libSamples; i++ {
			libEnergy += float64(libDecoded[i]) * float64(libDecoded[i])
		}
		libRMS := math.Sqrt(libEnergy / float64(libSamples))
		t.Logf("libopus decoded: %d samples, RMS=%.6f (expected ~%.3f)", libSamples, libRMS, amp*0.707)
	}

	// Also encode with libopus for comparison
	pcm32 := make([]float32, frameSize)
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)

	libEncoded, n := libEnc.EncodeFloat(pcm32, frameSize)
	if n < 0 {
		t.Fatalf("libopus encode failed: %d", n)
	}

	t.Logf("libopus encoded: %d bytes", len(libEncoded))
	t.Logf("First 32 bytes: %02x", libEncoded[:minIntBC(32, len(libEncoded))])

	// Compare (skipping TOC byte which libopus includes)
	t.Log("\nByte comparison (excluding TOC):")
	gopusData := encoded
	libData := libEncoded[1:] // Skip TOC

	t.Logf("gopus payload: %d bytes", len(gopusData))
	t.Logf("libopus payload: %d bytes", len(libData))

	matchCount := 0
	firstDiff := -1
	minLen := minIntBC(len(gopusData), len(libData))
	for i := 0; i < minLen; i++ {
		if gopusData[i] == libData[i] {
			matchCount++
		} else if firstDiff < 0 {
			firstDiff = i
		}
	}

	if firstDiff < 0 && len(gopusData) == len(libData) {
		t.Log("EXACT MATCH!")
	} else {
		t.Logf("First divergence at byte %d", firstDiff)
		t.Logf("Matching: %d/%d bytes", matchCount, minLen)

		// Show divergence context
		if firstDiff >= 0 && firstDiff < minLen {
			start := maxIntBC(0, firstDiff-3)
			end := minIntBC(minLen, firstDiff+10)
			t.Logf("Context around divergence (bytes %d-%d):", start, end)
			for i := start; i < end; i++ {
				var gStr, lStr, status string
				if i < len(gopusData) {
					gStr = fmt.Sprintf("0x%02X", gopusData[i])
				} else {
					gStr = "---"
				}
				if i < len(libData) {
					lStr = fmt.Sprintf("0x%02X", libData[i])
				} else {
					lStr = "---"
				}
				if i == firstDiff {
					status = "<-- FIRST DIVERGE"
				} else if i < len(gopusData) && i < len(libData) && gopusData[i] != libData[i] {
					status = "<-- mismatch"
				}
				t.Logf("  Byte %3d: gopus=%s libopus=%s %s", i, gStr, lStr, status)
			}
		}
	}
}

// TestByteCompare_GopusDecodeGopusEncoded tests that gopus can decode what it encodes.
func TestByteCompare_GopusDecodeGopusEncoded(t *testing.T) {
	frameSize := 960
	channels := 1
	freq := 440.0

	// Generate test signal
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2.0*math.Pi*freq*float64(i)/48000.0)
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)
	encoded, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	t.Logf("Encoded %d bytes", len(encoded))
	t.Logf("First 32 bytes: %v", encoded[:minIntBC(32, len(encoded))])

	// Decode with gopus
	decoder := celt.NewDecoder(channels)
	decoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("Gopus decode failed: %v", err)
	}

	// Compute mean
	var gopusMean float64
	for _, s := range decoded {
		gopusMean += math.Abs(s)
	}
	gopusMean /= float64(len(decoded))
	t.Logf("Gopus decoded mean: %.6f (should be ~0.3)", gopusMean)

	if gopusMean < 0.01 {
		t.Errorf("Gopus self-decode produces silence (mean=%.6f)", gopusMean)
	}
}

// TestByteCompare_LibopusDecodeGopusEncoded tests that libopus can decode what gopus encodes.
func TestByteCompare_LibopusDecodeGopusEncoded(t *testing.T) {
	frameSize := 960
	channels := 1
	freq := 440.0

	// Generate test signal
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2.0*math.Pi*freq*float64(i)/48000.0)
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)
	encoded, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Add TOC byte for libopus
	// Config 31 = CELT FB 20ms, mono, code 0
	toc := byte((31 << 3) | 0)
	packet := append([]byte{toc}, encoded...)

	t.Logf("Packet for libopus: TOC=0x%02X, total %d bytes", toc, len(packet))
	t.Logf("Packet first 32 bytes: %v", packet[:minIntBC(32, len(packet))])

	// Decode with libopus
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	// Compute mean
	var libMean float64
	for i := 0; i < libSamples; i++ {
		libMean += math.Abs(float64(libDecoded[i]))
	}
	libMean /= float64(libSamples)
	t.Logf("Libopus decoded mean: %.10f (should be ~0.3)", libMean)

	if libMean < 0.001 {
		t.Errorf("SILENCE: libopus decode of gopus-encoded packet produces silence (mean=%.10f)", libMean)
	}
}

// TestByteCompare_SilenceFrame tests silence frame encoding.
func TestByteCompare_SilenceFrame(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate silence
	pcm := make([]float64, frameSize)

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)
	encoded, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	t.Logf("Silence frame encoded to %d bytes: %v", len(encoded), encoded)

	// Add TOC byte
	toc := byte((31 << 3) | 0)
	packet := append([]byte{toc}, encoded...)

	// Decode with libopus
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	// For silence, energy should be very low
	var libEnergy float64
	for i := 0; i < libSamples; i++ {
		libEnergy += float64(libDecoded[i]) * float64(libDecoded[i])
	}
	t.Logf("Libopus decoded silence energy: %.10f", libEnergy)
}

func maxIntBC(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minIntBC(a, b int) int {
	if a < b {
		return a
	}
	return b
}
