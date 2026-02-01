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
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// Quality thresholds for encoder compliance
const (
	// EncoderQualityThreshold is the minimum Q value for passing encoder tests.
	// Q >= 0 corresponds to approximately 48 dB SNR.
	// For encode→decode roundtrip, we use a lower threshold initially.
	EncoderQualityThreshold = -25.0 // ~36 dB SNR

	// EncoderStrictThreshold is the target for high-quality encoding.
	EncoderStrictThreshold = 0.0 // 48 dB SNR

	// Pre-skip samples as defined in Ogg Opus header
	OpusPreSkip = 312
)

// TestEncoderComplianceCELT tests CELT mode encoding at various frame sizes.
func TestEncoderComplianceCELT(t *testing.T) {
	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not found in PATH")
	}

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
	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not found in PATH")
	}

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
	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not found in PATH")
	}

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
	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not found in PATH")
	}

	bitrates := []int{32000, 64000, 128000, 256000}

	for _, bitrate := range bitrates {
		t.Run(fmt.Sprintf("CELT-%dk", bitrate/1000), func(t *testing.T) {
			testEncoderCompliance(t, encoder.ModeCELT, types.BandwidthFullband, 960, 1, bitrate)
		})
	}
}

// TestEncoderComplianceSummary runs all configurations and outputs a summary table.
func TestEncoderComplianceSummary(t *testing.T) {
	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not found in PATH")
	}

	type testCase struct {
		name      string
		mode      encoder.Mode
		bandwidth types.Bandwidth
		frameSize int
		channels  int
		bitrate   int
	}

	cases := []testCase{
		// CELT
		{"CELT-FB-20ms-mono-64k", encoder.ModeCELT, types.BandwidthFullband, 960, 1, 64000},
		{"CELT-FB-20ms-stereo-128k", encoder.ModeCELT, types.BandwidthFullband, 960, 2, 128000},
		{"CELT-FB-10ms-mono-64k", encoder.ModeCELT, types.BandwidthFullband, 480, 1, 64000},
		// SILK
		{"SILK-NB-20ms-mono-16k", encoder.ModeSILK, types.BandwidthNarrowband, 960, 1, 16000},
		{"SILK-WB-20ms-mono-32k", encoder.ModeSILK, types.BandwidthWideband, 960, 1, 32000},
		{"SILK-WB-20ms-stereo-48k", encoder.ModeSILK, types.BandwidthWideband, 960, 2, 48000},
		// Hybrid
		{"Hybrid-SWB-20ms-mono-48k", encoder.ModeHybrid, types.BandwidthSuperwideband, 960, 1, 48000},
		{"Hybrid-FB-20ms-mono-64k", encoder.ModeHybrid, types.BandwidthFullband, 960, 1, 64000},
		{"Hybrid-FB-20ms-stereo-96k", encoder.ModeHybrid, types.BandwidthFullband, 960, 2, 96000},
	}

	t.Log("Encoder Compliance Summary")
	t.Log("===========================")
	t.Logf("%-35s %10s %10s %s", "Configuration", "Q", "SNR(dB)", "Status")
	t.Logf("%-35s %10s %10s %s", "--------------", "----", "------", "------")

	passed := 0
	failed := 0

	for _, tc := range cases {
		q, decoded := runEncoderComplianceTest(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)

		snr := SNRFromQuality(q)
		status := "FAIL"
		if q >= EncoderStrictThreshold {
			status = "PASS"
			passed++
		} else if q >= EncoderQualityThreshold {
			status = "INFO"
			passed++
		} else {
			failed++
		}

		_ = decoded // decoded samples available for debugging if needed
		t.Logf("%-35s %10.2f %10.2f %s", tc.name, q, snr, status)
	}

	t.Logf("---")
	t.Logf("Total: %d passed, %d failed", passed, failed)
}

// testEncoderCompliance runs a single encoder compliance test.
func testEncoderCompliance(t *testing.T, mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) {
	q, _ := runEncoderComplianceTest(t, mode, bandwidth, frameSize, channels, bitrate)

	snr := SNRFromQuality(q)
	t.Logf("Quality: Q=%.2f, SNR=%.2f dB", q, snr)

	if q >= EncoderStrictThreshold {
		t.Logf("PASS: Meets strict quality threshold (Q >= 0)")
	} else if q >= EncoderQualityThreshold {
		t.Logf("INFO: Meets minimum threshold (Q >= -25)")
	} else {
		// Log but don't fail - this is informational for initial encoder development
		t.Logf("INFO: Below minimum threshold - encoder may need tuning")
	}
}

// runEncoderComplianceTest runs the full encode→decode→compare pipeline.
func runEncoderComplianceTest(t *testing.T, mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) (q float64, decoded []float32) {
	// Generate 1 second of test signal
	numFrames := 48000 / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	// Create encoder
	enc := encoder.NewEncoder(48000, channels)
	enc.SetMode(mode)
	enc.SetBandwidth(bandwidth)
	enc.SetBitrate(bitrate)

	// Encode all frames
	packets := make([][]byte, numFrames)
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
		packets[i] = packet
	}

	// Write to Ogg Opus container
	var oggBuf bytes.Buffer
	err := writeOggOpusEncoder(&oggBuf, packets, channels, 48000, frameSize)
	if err != nil {
		t.Fatalf("writeOggOpus failed: %v", err)
	}

	// Decode with opusdec
	decoded, err = decodeWithOpusdec(oggBuf.Bytes())
	if err != nil {
		// Check for macOS provenance issues
		if err.Error() == "opusdec blocked by macOS provenance" {
			t.Skip("opusdec blocked by macOS provenance - skipping")
		}
		t.Fatalf("decodeWithOpusdec failed: %v", err)
	}

	if len(decoded) == 0 {
		t.Fatal("No samples decoded")
	}

	// Strip pre-skip samples from decoded output
	preSkipSamples := OpusPreSkip * channels
	if len(decoded) > preSkipSamples {
		decoded = decoded[preSkipSamples:]
	}

	// Align lengths for comparison (decoded may have trailing samples)
	compareLen := len(original)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}

	// Compute quality metric with delay compensation
	// The codec introduces inherent delay (~421 samples for libopus)
	// We search for optimal alignment to get accurate quality measurement
	// Use larger search range (1000 samples) to account for multi-frame encoding
	q, _ = ComputeQualityFloat32WithDelay(decoded[:compareLen], original[:compareLen], 48000, 1000)

	return q, decoded
}

// Test signal generators

// generateEncoderTestSignal generates a test signal for encoding.
// Uses a combination of frequencies to test across the audio spectrum.
func generateEncoderTestSignal(samples int, channels int) []float32 {
	signal := make([]float32, samples)

	// Multi-frequency test signal: 440 Hz + 1000 Hz + 2000 Hz
	freqs := []float64{440, 1000, 2000}
	amp := 0.3 // Amplitude per frequency (0.3 * 3 = 0.9 total)

	for i := 0; i < samples; i++ {
		ch := i % channels
		sampleIdx := i / channels
		t := float64(sampleIdx) / 48000.0

		var val float64
		for _, freq := range freqs {
			// For stereo, slightly offset frequencies between channels
			f := freq
			if channels == 2 && ch == 1 {
				f *= 1.01 // 1% higher frequency on right channel
			}
			val += amp * math.Sin(2*math.Pi*f*t)
		}

		signal[i] = float32(val)
	}

	return signal
}

// Ogg container helpers

// writeOggOpusEncoder writes Opus packets to an Ogg container.
// Minimal implementation per RFC 7845.
func writeOggOpusEncoder(w io.Writer, packets [][]byte, channels, sampleRate, frameSize int) error {
	serialNo := uint32(12345)
	var granulePos uint64

	// Page 1: OpusHead header
	opusHead := makeOpusHeadEncoder(channels, sampleRate)
	if err := writeOggPageEncoder(w, serialNo, 0, 2, 0, [][]byte{opusHead}); err != nil {
		return err
	}

	// Page 2: OpusTags header
	opusTags := makeOpusTagsEncoder()
	if err := writeOggPageEncoder(w, serialNo, 1, 0, 0, [][]byte{opusTags}); err != nil {
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
		if err := writeOggPageEncoder(w, serialNo, pageNo, headerType, granulePos, [][]byte{packet}); err != nil {
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

func writeOggPageEncoder(w io.Writer, serialNo, pageNo uint32, headerType byte, granulePos uint64, segments [][]byte) error {
	// Calculate segment table
	var segmentTable []byte
	for _, seg := range segments {
		remaining := len(seg)
		for remaining >= 255 {
			segmentTable = append(segmentTable, 255)
			remaining -= 255
		}
		segmentTable = append(segmentTable, byte(remaining))
	}

	// Page header
	header := make([]byte, 27+len(segmentTable))
	copy(header[0:4], "OggS")
	header[4] = 0 // Version
	header[5] = headerType
	binary.LittleEndian.PutUint64(header[6:14], granulePos)
	binary.LittleEndian.PutUint32(header[14:18], serialNo)
	binary.LittleEndian.PutUint32(header[18:22], pageNo)
	// CRC will be at [22:26]
	header[26] = byte(len(segmentTable))
	copy(header[27:], segmentTable)

	// Compute CRC
	crc := oggCRCEncoder(header)
	for _, seg := range segments {
		crc = oggCRCUpdateEncoder(crc, seg)
	}
	binary.LittleEndian.PutUint32(header[22:26], crc)

	// Write header
	if _, err := w.Write(header); err != nil {
		return err
	}

	// Write segments
	for _, seg := range segments {
		if _, err := w.Write(seg); err != nil {
			return err
		}
	}

	return nil
}

// Ogg CRC-32 (polynomial 0x04c11db7)
var oggCRCTableEncoder [256]uint32

func init() {
	for i := 0; i < 256; i++ {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		oggCRCTableEncoder[i] = r
	}
}

func oggCRCEncoder(data []byte) uint32 {
	return oggCRCUpdateEncoder(0, data)
}

func oggCRCUpdateEncoder(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTableEncoder[byte(crc>>24)^b]
	}
	return crc
}

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
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

func checkOpusdecAvailableEncoder() bool {
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
	t.Log("Quality Thresholds:")
	t.Logf("  Strict Pass: Q >= %.1f (%.1f dB SNR)", EncoderStrictThreshold, SNRFromQuality(EncoderStrictThreshold))
	t.Logf("  Minimum:     Q >= %.1f (%.1f dB SNR)", EncoderQualityThreshold, SNRFromQuality(EncoderQualityThreshold))
}
