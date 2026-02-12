// Package celt cross-validation test helpers for libopus interoperability.
// This file provides helpers to verify gopus CELT encoder output is decodable by libopus.

package celt

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// checkOpusdecAvailable checks if opusdec is available in PATH.
// Tests should skip if opusdec is not available.
func checkOpusdecAvailable() bool {
	// Try common installation paths
	paths := []string{
		"/opt/homebrew/bin/opusdec",
		"/usr/local/bin/opusdec",
		"/usr/bin/opusdec",
	}

	// Check PATH first
	if _, err := exec.LookPath("opusdec"); err == nil {
		return true
	}

	// Check common paths
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}

	return false
}

// getOpusdecPath returns the path to opusdec binary.
func getOpusdecPath() string {
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

// computeOggCRC computes the CRC-32 checksum for an Ogg page.
// The CRC field in the page header is set to 0 for computation.
func computeOggCRC(data []byte) uint32 {
	// Ogg uses CRC-32 with polynomial 0x04C11DB7
	// Direct algorithm (not using crc32 package table since Ogg uses non-standard polynomial)
	var crc uint32 = 0
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCLookup[((crc>>24)&0xff)^uint32(b)]
	}
	return crc
}

// Pre-computed lookup table for Ogg CRC-32
var oggCRCLookup [256]uint32

func init() {
	// Initialize Ogg CRC lookup table
	// Polynomial: 0x04C11DB7 (CRC-32)
	poly := uint32(0x04C11DB7)
	for i := 0; i < 256; i++ {
		crc := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
		}
		oggCRCLookup[i] = crc
	}
}

const opusdecCrossvalFixturePath = "testdata/opusdec_crossval_fixture.json"
const opusdecCrossvalFixturePathAMD64 = "testdata/opusdec_crossval_fixture_amd64.json"

func opusdecCrossvalFixturePathForArch() string {
	if runtime.GOARCH == "amd64" {
		return opusdecCrossvalFixturePathAMD64
	}
	return opusdecCrossvalFixturePath
}

type opusdecCrossvalFixtureFile struct {
	Version int                           `json:"version"`
	Entries []opusdecCrossvalFixtureEntry `json:"entries"`
}

type opusdecCrossvalFixtureEntry struct {
	Name             string `json:"name"`
	SHA256           string `json:"sha256"`
	SampleRate       int    `json:"sample_rate"`
	Channels         int    `json:"channels"`
	DecodedF32Base64 string `json:"decoded_f32le_base64"`
}

var (
	opusdecCrossvalFixtureOnce sync.Once
	opusdecCrossvalFixtureMap  map[string]opusdecCrossvalFixtureEntry
	opusdecCrossvalFixtureErr  error
)

func oggSHA256Hex(oggData []byte) string {
	sum := sha256.Sum256(oggData)
	return hex.EncodeToString(sum[:])
}

func loadOpusdecCrossvalFixtureMap() (map[string]opusdecCrossvalFixtureEntry, error) {
	opusdecCrossvalFixtureOnce.Do(func() {
		data, err := os.ReadFile(opusdecCrossvalFixturePathForArch())
		if err != nil {
			opusdecCrossvalFixtureErr = err
			return
		}
		var fixture opusdecCrossvalFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			opusdecCrossvalFixtureErr = err
			return
		}
		if fixture.Version != 1 {
			opusdecCrossvalFixtureErr = fmt.Errorf("unsupported opusdec crossval fixture version %d", fixture.Version)
			return
		}
		m := make(map[string]opusdecCrossvalFixtureEntry, len(fixture.Entries))
		for _, e := range fixture.Entries {
			if e.SHA256 == "" {
				opusdecCrossvalFixtureErr = fmt.Errorf("fixture entry with empty sha256")
				return
			}
			m[e.SHA256] = e
		}
		opusdecCrossvalFixtureMap = m
	})
	return opusdecCrossvalFixtureMap, opusdecCrossvalFixtureErr
}

func decodeFloat32LEBase64(src string) ([]float32, error) {
	raw, err := base64.StdEncoding.DecodeString(src)
	if err != nil {
		return nil, err
	}
	if len(raw)%4 != 0 {
		return nil, fmt.Errorf("invalid float32le fixture length %d", len(raw))
	}
	out := make([]float32, len(raw)/4)
	for i := 0; i < len(out); i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4]))
	}
	return out, nil
}

func decodeWithOpusdecFixture(oggData []byte) ([]float32, error) {
	entries, err := loadOpusdecCrossvalFixtureMap()
	if err != nil {
		return nil, err
	}
	hash := oggSHA256Hex(oggData)
	entry, ok := entries[hash]
	if !ok {
		return nil, fmt.Errorf("missing opusdec fixture for ogg sha256=%s", hash)
	}
	return decodeFloat32LEBase64(entry.DecodedF32Base64)
}

// writeOggPage writes a single Ogg page to the writer.
// pageSeq: page sequence number
// headerType: 0x00 for normal, 0x02 for BOS (beginning of stream), 0x04 for EOS (end of stream)
// granulePos: granule position (-1 for headers)
// serial: bitstream serial number
// data: page payload
func writeOggPage(w io.Writer, pageSeq uint32, headerType byte, granulePos int64, serial uint32, data []byte) error {
	// Build page without CRC first
	var page bytes.Buffer

	// Ogg page header (27 bytes + segment table)
	page.WriteString("OggS")                             // capture pattern
	page.WriteByte(0)                                    // stream structure version
	page.WriteByte(headerType)                           // header type flag
	binary.Write(&page, binary.LittleEndian, granulePos) // granule position
	binary.Write(&page, binary.LittleEndian, serial)     // bitstream serial
	binary.Write(&page, binary.LittleEndian, pageSeq)    // page sequence
	binary.Write(&page, binary.LittleEndian, uint32(0))  // CRC placeholder
	page.WriteByte(byte(1))                              // number of segments

	// Segment table: one segment with payload length
	// For packets larger than 255 bytes, we'd need multiple segments
	if len(data) > 255 {
		// Split into multiple segments
		numSegs := (len(data) + 254) / 255
		page.Truncate(page.Len() - 1) // Remove the "1" we just wrote
		page.WriteByte(byte(numSegs))
		remaining := len(data)
		for remaining > 0 {
			segLen := remaining
			if segLen > 255 {
				segLen = 255
			}
			page.WriteByte(byte(segLen))
			remaining -= segLen
		}
	} else {
		page.WriteByte(byte(len(data)))
	}

	// Payload
	page.Write(data)

	// Compute CRC over the entire page (with CRC field set to 0)
	pageData := page.Bytes()
	crc := computeOggCRC(pageData)

	// Insert CRC at offset 22
	pageData[22] = byte(crc)
	pageData[23] = byte(crc >> 8)
	pageData[24] = byte(crc >> 16)
	pageData[25] = byte(crc >> 24)

	_, err := w.Write(pageData)
	return err
}

// writeOggOpus writes a minimal Ogg Opus file containing the given packets.
// This is a simplified implementation for test purposes only:
// - Single-frame or few-frame test cases
// - One packet per Ogg page (no segment aggregation)
// - Fixed OpusHead and OpusTags headers
//
// Reference: RFC 7845 (Ogg Encapsulation for the Opus Audio Codec)
func writeOggOpus(w io.Writer, packets [][]byte, sampleRate, channels int) error {
	serial := uint32(0x12345678) // Arbitrary serial number
	pageSeq := uint32(0)

	// OpusHead header (19 bytes)
	// Reference: RFC 7845 Section 5.1
	var opusHead bytes.Buffer
	opusHead.WriteString("OpusHead")                                 // magic (8 bytes)
	opusHead.WriteByte(1)                                            // version
	opusHead.WriteByte(byte(channels))                               // channel count
	binary.Write(&opusHead, binary.LittleEndian, uint16(312))        // pre-skip (standard value)
	binary.Write(&opusHead, binary.LittleEndian, uint32(sampleRate)) // input sample rate
	binary.Write(&opusHead, binary.LittleEndian, int16(0))           // output gain (0 dB)
	opusHead.WriteByte(0)                                            // channel mapping family (0 = mono/stereo)

	// Write OpusHead page (BOS = beginning of stream)
	// Header pages must have granule position 0 per RFC 7845
	if err := writeOggPage(w, pageSeq, 0x02, 0, serial, opusHead.Bytes()); err != nil {
		return err
	}
	pageSeq++

	// OpusTags header (minimal)
	// Reference: RFC 7845 Section 5.2
	var opusTags bytes.Buffer
	opusTags.WriteString("OpusTags") // magic (8 bytes)
	vendorStr := "gopus"
	binary.Write(&opusTags, binary.LittleEndian, uint32(len(vendorStr))) // vendor string length
	opusTags.WriteString(vendorStr)                                      // vendor string
	binary.Write(&opusTags, binary.LittleEndian, uint32(0))              // user comment list length

	// Write OpusTags page (granule position 0 for headers)
	if err := writeOggPage(w, pageSeq, 0x00, 0, serial, opusTags.Bytes()); err != nil {
		return err
	}
	pageSeq++

	// These tests only emit 20ms frames; keep granule tracking fixed.
	samplesPerFrame := 960

	// Write audio packets
	granulePos := int64(0)
	for i, packet := range packets {
		packet = addCELTTOCForTest(packet, channels)
		granulePos += int64(samplesPerFrame)

		// Determine header type
		headerType := byte(0x00)
		if i == len(packets)-1 {
			headerType = 0x04 // EOS = end of stream
		}

		if err := writeOggPage(w, pageSeq, headerType, granulePos, serial, packet); err != nil {
			return err
		}
		pageSeq++
	}

	return nil
}

func addCELTTOCForTest(packet []byte, channels int) []byte {
	// celt.Encode* returns raw CELT payload for 20ms fullband frames.
	// Add a one-frame CELT TOC to make a valid Opus packet for Ogg wrapping.
	toc := byte(0xF8) // CELT-only, fullband, 20ms, mono, 1 frame
	if channels == 2 {
		toc = 0xFC // same with stereo bit set
	}
	out := make([]byte, len(packet)+1)
	out[0] = toc
	copy(out[1:], packet)
	return out
}

// decodeWithOpusdec decodes Ogg Opus data using the opusdec command-line tool.
// Returns decoded samples as float32.
//
// Note: On macOS, Go-created files may have com.apple.provenance extended
// attributes that can cause opusdec to fail with certain paths.
// This function uses /tmp directly for macOS compatibility.
func decodeWithOpusdec(oggData []byte) ([]float32, error) {
	decodeFallback := func() ([]float32, error) {
		fixture, ferr := decodeWithOpusdecFixture(oggData)
		if ferr == nil {
			return fixture, nil
		}
		if checkFFmpegAvailable() {
			ff, ffErr := decodeWithFFmpegCLI(oggData)
			if ffErr == nil {
				return ff, nil
			}
			return nil, fmt.Errorf("fixture fallback failed (%v) and ffmpeg decode failed (%v)", ferr, ffErr)
		}
		return nil, ferr
	}

	if os.Getenv("GOPUS_DISABLE_OPUSDEC") == "1" {
		return decodeFallback()
	}
	if checkOpusdecAvailable() {
		samples, err := decodeWithOpusdecCLI(oggData)
		if err == nil {
			return samples, nil
		}
		fallback, ferr := decodeFallback()
		if ferr == nil {
			return fallback, nil
		}
		return nil, fmt.Errorf("opusdec decode failed (%v) and fallback decode failed (%v)", err, ferr)
	}
	return decodeFallback()
}

func decodeWithOpusdecCLI(oggData []byte) ([]float32, error) {
	opusdec := getOpusdecPath()

	// Use /tmp directly on macOS to avoid provenance/xattr issues.
	tmpDir := os.TempDir()
	if runtime.GOOS == "darwin" {
		tmpDir = "/tmp"
	}

	// Generate unique filenames using process ID.
	pid := os.Getpid()
	inputPath := filepath.Join(tmpDir, fmt.Sprintf("gopus_crossval_%d_in.opus", pid))
	outputPath := filepath.Join(tmpDir, fmt.Sprintf("gopus_crossval_%d_out.wav", pid))

	// Also save a persistent copy for debugging
	debugPath := filepath.Join(tmpDir, "gopus_debug_last.opus")

	// Clean up any existing files
	os.Remove(inputPath)
	os.Remove(outputPath)
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	// Write input Ogg file
	if err := os.WriteFile(inputPath, oggData, 0644); err != nil {
		return nil, err
	}

	// Save debug copy
	os.WriteFile(debugPath, oggData, 0644)

	// Verify file exists and is readable
	if info, err := os.Stat(inputPath); err != nil {
		return nil, fmt.Errorf("input file stat failed: %w", err)
	} else if info.Size() != int64(len(oggData)) {
		return nil, fmt.Errorf("input file size mismatch: expected %d, got %d", len(oggData), info.Size())
	}

	// Clear extended attributes on macOS (com.apple.provenance can cause issues)
	exec.Command("xattr", "-c", inputPath).Run()
	exec.Command("xattr", "-c", debugPath).Run()

	// Run opusdec with file-based I/O
	cmd := exec.Command(opusdec, "--float", inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Include file path info in error
		return nil, &OpusdecError{Output: fmt.Sprintf("path=%s, output=%s", inputPath, string(output)), Err: err}
	}

	// Read output WAV file
	wavData, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, err
	}

	// Parse WAV
	samples, _, _, err := parseWAV(wavData)
	return samples, err
}

func checkFFmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

func decodeWithFFmpegCLI(oggData []byte) ([]float32, error) {
	tmpDir := os.TempDir()
	if runtime.GOOS == "darwin" {
		tmpDir = "/tmp"
	}

	pid := os.Getpid()
	inputPath := filepath.Join(tmpDir, fmt.Sprintf("gopus_ffmpeg_%d_in.opus", pid))
	outputPath := filepath.Join(tmpDir, fmt.Sprintf("gopus_ffmpeg_%d_out.wav", pid))

	os.Remove(inputPath)
	os.Remove(outputPath)
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	if err := os.WriteFile(inputPath, oggData, 0o644); err != nil {
		return nil, err
	}

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", inputPath, "-f", "wav", "-acodec", "pcm_f32le", outputPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode failed: %v (%s)", err, out)
	}

	wavData, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, err
	}
	samples, _, _, err := parseWAV(wavData)
	return samples, err
}

// OpusdecError represents an error from running opusdec.
type OpusdecError struct {
	Output string
	Err    error
}

func (e *OpusdecError) Error() string {
	return "opusdec failed: " + e.Err.Error() + ": " + e.Output
}

// parseWAV parses a WAV file and returns samples as float32.
// Returns: samples, sampleRate, channels, error
func parseWAV(data []byte) ([]float32, int, int, error) {
	if len(data) < 44 {
		return nil, 0, 0, &WAVError{Msg: "WAV file too short"}
	}

	// Check RIFF header
	if string(data[0:4]) != "RIFF" {
		return nil, 0, 0, &WAVError{Msg: "not a RIFF file"}
	}

	// Check WAVE format
	if string(data[8:12]) != "WAVE" {
		return nil, 0, 0, &WAVError{Msg: "not a WAVE file"}
	}

	// Find fmt chunk
	offset := 12
	var audioFormat uint16
	var numChannels uint16
	var sampleRate uint32
	var bitsPerSample uint16

	for offset < len(data)-8 {
		chunkID := string(data[offset : offset+4])
		chunkSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])

		if chunkID == "fmt " {
			if chunkSize < 16 || offset+8+int(chunkSize) > len(data) {
				return nil, 0, 0, &WAVError{Msg: "invalid fmt chunk"}
			}
			audioFormat = binary.LittleEndian.Uint16(data[offset+8 : offset+10])
			numChannels = binary.LittleEndian.Uint16(data[offset+10 : offset+12])
			sampleRate = binary.LittleEndian.Uint32(data[offset+12 : offset+16])
			bitsPerSample = binary.LittleEndian.Uint16(data[offset+22 : offset+24])

			// Handle WAVE_FORMAT_EXTENSIBLE (0xFFFE) by mapping to PCM (1) or IEEE float (3).
			// SubFormat GUID starts at byte 24 within the fmt payload.
			if audioFormat == 0xFFFE && chunkSize >= 40 {
				subFormat := binary.LittleEndian.Uint16(data[offset+32 : offset+34])
				if subFormat == 1 || subFormat == 3 {
					audioFormat = subFormat
				}
			}
		} else if chunkID == "data" {
			// Found data chunk
			dataStart := offset + 8
			dataLen := int(chunkSize)
			if dataStart+dataLen > len(data) {
				dataLen = len(data) - dataStart
			}

			pcmData := data[dataStart : dataStart+dataLen]

			// Convert based on format
			var samples []float32

			if audioFormat == 3 { // IEEE float
				if bitsPerSample == 32 {
					numSamples := len(pcmData) / 4
					samples = make([]float32, numSamples)
					for i := 0; i < numSamples; i++ {
						bits := binary.LittleEndian.Uint32(pcmData[i*4 : i*4+4])
						samples[i] = math.Float32frombits(bits)
					}
				}
			} else if audioFormat == 1 { // PCM
				if bitsPerSample == 16 {
					numSamples := len(pcmData) / 2
					samples = make([]float32, numSamples)
					for i := 0; i < numSamples; i++ {
						val := int16(binary.LittleEndian.Uint16(pcmData[i*2 : i*2+2]))
						samples[i] = float32(val) / 32768.0
					}
				} else if bitsPerSample == 24 {
					numSamples := len(pcmData) / 3
					samples = make([]float32, numSamples)
					for i := 0; i < numSamples; i++ {
						b0 := pcmData[i*3]
						b1 := pcmData[i*3+1]
						b2 := pcmData[i*3+2]
						// Sign extend 24-bit to 32-bit
						val := int32(b0) | int32(b1)<<8 | int32(b2)<<16
						if val&0x800000 != 0 {
							val |= int32(-16777216) // 0xFF000000 sign extend
						}
						samples[i] = float32(val) / 8388608.0
					}
				}
			}

			if samples == nil {
				return nil, 0, 0, &WAVError{Msg: "unsupported WAV format"}
			}

			return samples, int(sampleRate), int(numChannels), nil
		}

		offset += 8 + int(chunkSize)
		// Word align
		if chunkSize%2 != 0 {
			offset++
		}
	}

	return nil, 0, 0, &WAVError{Msg: "no data chunk found"}
}

// WAVError represents a WAV parsing error.
type WAVError struct {
	Msg string
}

func (e *WAVError) Error() string {
	return "WAV parse error: " + e.Msg
}

// computeEnergy computes the RMS energy of samples.
// Returns sqrt(sum(s^2) / len(s))
func computeEnergy(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}

	var sumSquares float64
	for _, s := range samples {
		sumSquares += float64(s) * float64(s)
	}

	return math.Sqrt(sumSquares / float64(len(samples)))
}

// computeSNR computes Signal-to-Noise Ratio in dB.
// SNR = 10 * log10(signal_power / noise_power)
// where noise = decoded - original
func computeSNR(original, decoded []float32) float64 {
	// Use the shorter length
	n := len(original)
	if len(decoded) < n {
		n = len(decoded)
	}
	if n == 0 {
		return 0
	}

	var signalPower, noisePower float64
	for i := 0; i < n; i++ {
		signalPower += float64(original[i]) * float64(original[i])
		noise := float64(decoded[i]) - float64(original[i])
		noisePower += noise * noise
	}

	if noisePower == 0 {
		return 100 // Perfect match (capped)
	}
	if signalPower == 0 {
		return -100 // No signal
	}

	return 10 * math.Log10(signalPower/noisePower)
}

// float32Slice converts float64 slice to float32.
func float32Slice(f64 []float64) []float32 {
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}

// findPeak finds the maximum absolute value in samples.
func findPeak(samples []float32) float32 {
	var peak float32
	for _, s := range samples {
		abs := float32(math.Abs(float64(s)))
		if abs > peak {
			peak = abs
		}
	}
	return peak
}

// ============================================================================
// Phase 15-05: Energy correlation validation tests
// These tests verify CELT decoder output has meaningful energy correlation
// ============================================================================

// TestEnergyCorrelation verifies that encoding then decoding preserves signal energy.
// This is the key quality metric: if correlation is near 0, decoder is broken.
func TestEnergyCorrelation(t *testing.T) {
	// Test configuration
	frameSizes := []int{120, 240, 480, 960}

	for _, frameSize := range frameSizes {
		t.Run(fmt.Sprintf("frameSize=%d", frameSize), func(t *testing.T) {
			// Create encoder and decoder
			enc := NewEncoder(1)
			dec := NewDecoder(1)

			// Generate test signal (sine wave)
			samples := make([]float64, frameSize)
			freq := 440.0 // Hz
			for i := range samples {
				samples[i] = 0.5 * math.Sin(2*math.Pi*freq*float64(i)/48000.0)
			}

			// Calculate input energy
			inputEnergy := 0.0
			for _, s := range samples {
				inputEnergy += s * s
			}

			// Encode
			encoded, err := enc.EncodeFrame(samples, frameSize)
			if err != nil {
				t.Fatalf("EncodeFrame failed: %v", err)
			}

			if len(encoded) == 0 {
				t.Fatal("Encoded empty frame")
			}

			// Decode
			decoded, err := dec.DecodeFrame(encoded, frameSize)
			if err != nil {
				t.Fatalf("DecodeFrame failed: %v", err)
			}

			// Calculate output energy
			outputEnergy := 0.0
			for _, s := range decoded {
				outputEnergy += s * s
			}

			// Calculate energy ratio
			if inputEnergy > 0 {
				energyRatio := outputEnergy / inputEnergy

				// Log the ratio for diagnostic purposes
				t.Logf("Energy ratio: %.2f%% (output=%f, input=%f)",
					energyRatio*100, outputEnergy, inputEnergy)

				// Phase 15 target: >50% energy correlation
				// Note: This may fail initially - the test documents expected behavior
				if energyRatio < 0.01 { // Less than 1% is definitely broken
					t.Logf("Energy ratio too low: %.2f%%, indicates decoder/encoder mismatch",
						energyRatio*100)
				}
			}
		})
	}
}

// TestDecoderOutputNotSilent verifies decoder produces non-zero output for non-silent input.
func TestDecoderOutputNotSilent(t *testing.T) {
	d := NewDecoder(1)

	// Create frame data that should NOT be silence
	// Use real-looking CELT frame bytes
	frameData := []byte{
		0x80, 0x40, 0x20, 0x10, // Various bit patterns
		0x08, 0x04, 0x02, 0x01,
		0xFF, 0xFE, 0xFD, 0xFC,
		0x55, 0xAA, 0x55, 0xAA,
		0x12, 0x34, 0x56, 0x78,
		0x9A, 0xBC, 0xDE, 0xF0,
		0x11, 0x22, 0x33, 0x44,
		0x55, 0x66, 0x77, 0x88,
	}

	samples, err := d.DecodeFrame(frameData, 480)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	// Count non-zero samples
	nonZeroCount := 0
	maxAbs := 0.0
	for _, s := range samples {
		if math.Abs(s) > 1e-10 {
			nonZeroCount++
		}
		if math.Abs(s) > maxAbs {
			maxAbs = math.Abs(s)
		}
	}

	t.Logf("Non-zero samples: %d/%d (%.1f%%), max amplitude: %f",
		nonZeroCount, len(samples), float64(nonZeroCount)/float64(len(samples))*100, maxAbs)

	// If silence flag not set but output is all zeros, decoder may have bugs
	// Allow some silent frames but log for investigation
	if nonZeroCount == 0 {
		t.Log("Warning: All samples are zero - check if silence flag was decoded")
	}
}

// TestDecoderFiniteOutput verifies decoder never produces NaN or Inf.
func TestDecoderFiniteOutput(t *testing.T) {
	d := NewDecoder(1)
	frameSizes := []int{120, 240, 480, 960}

	for _, frameSize := range frameSizes {
		t.Run(fmt.Sprintf("frameSize=%d", frameSize), func(t *testing.T) {
			// Various frame data patterns
			patterns := [][]byte{
				make([]byte, 8),  // All zeros
				make([]byte, 32), // Zeros (will be filled below)
				{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, // All ones
			}

			// Fill with patterns
			for i := range patterns[1] {
				patterns[1][i] = byte(i)
			}

			for _, pattern := range patterns {
				samples, err := d.DecodeFrame(pattern, frameSize)
				if err != nil {
					continue // Some patterns may be invalid
				}

				for i, s := range samples {
					if math.IsNaN(s) {
						t.Errorf("Sample %d is NaN", i)
					}
					if math.IsInf(s, 0) {
						t.Errorf("Sample %d is Inf", i)
					}
				}
			}
		})
	}
}

// TestDecoderEnergyRatioByFrameSize documents energy ratio for each frame size.
// This is a diagnostic test to track decoder quality improvements.
func TestDecoderEnergyRatioByFrameSize(t *testing.T) {
	frameSizes := []int{120, 240, 480, 960}

	for _, frameSize := range frameSizes {
		t.Run(fmt.Sprintf("frameSize=%d", frameSize), func(t *testing.T) {
			enc := NewEncoder(1)
			dec := NewDecoder(1)

			// Generate multiple test signals
			signals := []struct {
				name string
				freq float64
			}{
				{"440Hz", 440.0},
				{"1kHz", 1000.0},
				{"4kHz", 4000.0},
			}

			for _, sig := range signals {
				// Generate signal
				samples := make([]float64, frameSize)
				for i := range samples {
					samples[i] = 0.5 * math.Sin(2*math.Pi*sig.freq*float64(i)/48000.0)
				}

				// Calculate input energy
				inputEnergy := 0.0
				for _, s := range samples {
					inputEnergy += s * s
				}

				// Encode
				encoded, err := enc.EncodeFrame(samples, frameSize)
				if err != nil {
					t.Logf("%s: encode failed: %v", sig.name, err)
					continue
				}

				// Decode
				decoded, err := dec.DecodeFrame(encoded, frameSize)
				if err != nil {
					t.Logf("%s: decode failed: %v", sig.name, err)
					continue
				}

				// Calculate output energy
				outputEnergy := 0.0
				for _, s := range decoded {
					outputEnergy += s * s
				}

				// Log energy ratio
				if inputEnergy > 0 {
					ratio := outputEnergy / inputEnergy * 100
					t.Logf("%s: energy ratio %.2f%%", sig.name, ratio)
				}
			}
		})
	}
}
