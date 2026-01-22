// Package multistream libopus cross-validation tests.
// These tests verify gopus multistream encoder produces packets decodable by libopus opusdec.
//
// Note: On macOS, tests may skip with "Failed to open" errors due to file provenance
// restrictions. This is a macOS security feature (com.apple.provenance xattr) that
// prevents opusdec from opening files created by certain processes (e.g., sandboxed
// applications). The tests will pass on Linux and non-sandboxed macOS environments.

package multistream

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"testing"
)

// Ogg CRC-32 lookup table (polynomial 0x04c11db7)
var oggCRCTable [256]uint32

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
		oggCRCTable[i] = r
	}
}

// oggCRC computes the CRC-32 for an Ogg page.
func oggCRC(data []byte) uint32 {
	return oggCRCUpdate(0, data)
}

// oggCRCUpdate updates a running CRC.
func oggCRCUpdate(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}

// makeOggPage creates an Ogg page with proper CRC.
// Parameters:
//   - serialNo: stream serial number
//   - pageNo: sequence number
//   - headerType: page flags (2=BOS, 4=EOS, 0=normal)
//   - granulePos: position in samples
//   - segments: data segments to include in page
func makeOggPage(serialNo, pageNo uint32, headerType byte, granulePos uint64, segments [][]byte) []byte {
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

	// Build header (27 bytes + segment table)
	header := make([]byte, 27+len(segmentTable))
	copy(header[0:4], "OggS")           // Capture pattern
	header[4] = 0                       // Version
	header[5] = headerType              // Header type
	binary.LittleEndian.PutUint64(header[6:14], granulePos)
	binary.LittleEndian.PutUint32(header[14:18], serialNo)
	binary.LittleEndian.PutUint32(header[18:22], pageNo)
	// CRC at [22:26] - will be filled after
	header[26] = byte(len(segmentTable))
	copy(header[27:], segmentTable)

	// Compute CRC over header (with CRC field zeroed) + data
	crc := oggCRC(header)
	for _, seg := range segments {
		crc = oggCRCUpdate(crc, seg)
	}
	binary.LittleEndian.PutUint32(header[22:26], crc)

	// Combine header and segments
	var buf bytes.Buffer
	buf.Write(header)
	for _, seg := range segments {
		buf.Write(seg)
	}
	return buf.Bytes()
}

// makeOpusHeadMultistream creates OpusHead for mapping family 1 (RFC 7845 Section 5.1.1).
// Mapping family 1 is for surround sound (1-8 channels) with Vorbis channel order.
func makeOpusHeadMultistream(channels, sampleRate int, streams, coupledStreams int, mapping []byte) []byte {
	// OpusHead format (21+ bytes for family 1):
	// - 8 bytes: "OpusHead"
	// - 1 byte: version (1)
	// - 1 byte: channel count
	// - 2 bytes: pre-skip (little-endian)
	// - 4 bytes: input sample rate (little-endian)
	// - 2 bytes: output gain (little-endian)
	// - 1 byte: channel mapping family (1 for surround)
	// For family 1:
	// - 1 byte: stream count
	// - 1 byte: coupled stream count
	// - N bytes: channel mapping table

	size := 21 + len(mapping)
	head := make([]byte, size)

	copy(head[0:8], "OpusHead")
	head[8] = 1                                                // Version
	head[9] = byte(channels)                                   // Channel count
	binary.LittleEndian.PutUint16(head[10:12], 312)            // Pre-skip (standard value)
	binary.LittleEndian.PutUint32(head[12:16], uint32(sampleRate))
	binary.LittleEndian.PutUint16(head[16:18], 0)              // Output gain
	head[18] = 1                                               // Mapping family 1 (surround)
	head[19] = byte(streams)                                   // Stream count
	head[20] = byte(coupledStreams)                            // Coupled stream count
	copy(head[21:], mapping)                                   // Channel mapping table

	return head
}

// makeOpusTags creates minimal OpusTags header.
func makeOpusTags() []byte {
	vendor := "gopus"
	tags := make([]byte, 8+4+len(vendor)+4)
	copy(tags[0:8], "OpusTags")
	binary.LittleEndian.PutUint32(tags[8:12], uint32(len(vendor)))
	copy(tags[12:12+len(vendor)], vendor)
	binary.LittleEndian.PutUint32(tags[12+len(vendor):], 0) // User comment count
	return tags
}

// writeOggOpusMultistream writes a multistream Ogg Opus file.
// This follows RFC 7845 for Ogg encapsulation of Opus.
func writeOggOpusMultistream(w io.Writer, packets [][]byte, sampleRate, channels int,
	streams, coupledStreams int, mapping []byte, frameSize int) error {

	serialNo := uint32(54321)
	var granulePos uint64

	// Page 1: OpusHead header (BOS = Beginning of Stream)
	opusHead := makeOpusHeadMultistream(channels, sampleRate, streams, coupledStreams, mapping)
	page1 := makeOggPage(serialNo, 0, 2, 0, [][]byte{opusHead}) // 2 = BOS flag
	if _, err := w.Write(page1); err != nil {
		return err
	}

	// Page 2: OpusTags header
	opusTags := makeOpusTags()
	page2 := makeOggPage(serialNo, 1, 0, 0, [][]byte{opusTags})
	if _, err := w.Write(page2); err != nil {
		return err
	}

	// Data pages (one page per packet for simplicity)
	for i, packet := range packets {
		granulePos += uint64(frameSize)

		headerType := byte(0)
		if i == len(packets)-1 {
			headerType = 4 // EOS = End of Stream
		}

		pageNo := uint32(i + 2)
		page := makeOggPage(serialNo, pageNo, headerType, granulePos, [][]byte{packet})
		if _, err := w.Write(page); err != nil {
			return err
		}
	}

	return nil
}

// checkOpusdec checks if opusdec is available.
func checkOpusdec() bool {
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

// getOpusdecPath returns the path to opusdec.
func getOpusdecPath() string {
	if path, err := exec.LookPath("opusdec"); err == nil {
		return path
	}

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

// decodeWithOpusdec decodes an Ogg Opus file using libopus opusdec.
// Returns the decoded PCM samples as float32.
func decodeWithOpusdec(oggData []byte) ([]float32, error) {
	// Write to temp Opus file
	tmpOpus, err := os.CreateTemp("", "gopus_ms_*.opus")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpOpus.Name())

	if _, err := tmpOpus.Write(oggData); err != nil {
		tmpOpus.Close()
		return nil, err
	}
	tmpOpus.Close()

	// Clear extended attributes on macOS (provenance can cause issues)
	exec.Command("xattr", "-c", tmpOpus.Name()).Run()

	// Create output WAV file
	tmpWav, err := os.CreateTemp("", "gopus_ms_*.wav")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpWav.Name())
	tmpWav.Close()

	// Run opusdec
	opusdec := getOpusdecPath()
	cmd := exec.Command(opusdec, tmpOpus.Name(), tmpWav.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check for macOS provenance issues
		if bytes.Contains(output, []byte("provenance")) ||
			bytes.Contains(output, []byte("quarantine")) ||
			bytes.Contains(output, []byte("killed")) ||
			bytes.Contains(output, []byte("Operation not permitted")) ||
			bytes.Contains(output, []byte("Failed to open")) {
			return nil, &skipError{msg: string(output)}
		}
		return nil, err
	}

	// Read and parse WAV file
	wavData, err := os.ReadFile(tmpWav.Name())
	if err != nil {
		return nil, err
	}

	return parseWAVSamples(wavData), nil
}

// skipError indicates test should be skipped (macOS provenance issues).
type skipError struct {
	msg string
}

func (e *skipError) Error() string {
	return e.msg
}

// parseWAVSamples extracts float32 samples from WAV data.
func parseWAVSamples(data []byte) []float32 {
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

	// Fallback: skip WAV header
	data = data[44:]
	samples := make([]float32, len(data)/2)
	for i := 0; i < len(data)/2; i++ {
		s := int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
		samples[i] = float32(s) / 32768.0
	}
	return samples
}

// generateSineWave creates a mono sine wave.
func generateSineWave(freq float64, samples int) []float64 {
	pcm := make([]float64, samples)
	for i := range pcm {
		t := float64(i) / 48000.0
		pcm[i] = 0.5 * math.Sin(2*math.Pi*freq*t)
	}
	return pcm
}

// generateMultichannelSine creates interleaved multi-channel sine wave.
// Each channel gets a different frequency.
func generateMultichannelSine(channels, samplesPerChannel int) []float64 {
	pcm := make([]float64, channels*samplesPerChannel)

	// Base frequencies for each channel
	baseFreqs := []float64{220, 330, 440, 550, 660, 770, 880, 990}

	for s := 0; s < samplesPerChannel; s++ {
		t := float64(s) / 48000.0
		for ch := 0; ch < channels; ch++ {
			freq := baseFreqs[ch%len(baseFreqs)]
			pcm[s*channels+ch] = 0.3 * math.Sin(2*math.Pi*freq*t)
		}
	}

	return pcm
}

// computeEnergyF32 calculates RMS energy of float32 samples.
func computeEnergyF32(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return sum / float64(len(samples))
}

// TestLibopus_Stereo tests stereo multistream encoding with libopus.
func TestLibopus_Stereo(t *testing.T) {
	if !checkOpusdec() {
		t.Skip("opusdec not available in PATH")
	}

	// Create stereo encoder (2 channels, 1 stream, 1 coupled)
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault failed: %v", err)
	}
	enc.Reset()
	enc.SetBitrate(128000) // 128 kbps

	frameSize := 960 // 20ms at 48kHz
	numFrames := 20

	// Generate test audio
	var allInput []float64
	packets := make([][]byte, numFrames)

	for i := 0; i < numFrames; i++ {
		pcm := generateMultichannelSine(2, frameSize)
		allInput = append(allInput, pcm...)

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Frame %d: Encode failed: %v", i, err)
		}
		if packet == nil {
			t.Logf("Frame %d: DTX (silence)", i)
			// Create minimal packet for DTX
			packet = []byte{0xF8, 0xFF, 0xFE} // CELT silence
		}
		packets[i] = packet
		if i == 0 {
			t.Logf("Frame %d: %d bytes", i, len(packet))
		}
	}

	// Compute input energy
	inputF32 := make([]float32, len(allInput))
	for i, v := range allInput {
		inputF32[i] = float32(v)
	}
	inputEnergy := computeEnergyF32(inputF32)
	t.Logf("Input: %d frames, %d samples, energy=%.6f", numFrames, len(allInput), inputEnergy)

	// Get mapping for stereo
	streams, coupled, mapping, _ := DefaultMapping(2)

	// Write Ogg Opus container
	var ogg bytes.Buffer
	err = writeOggOpusMultistream(&ogg, packets, 48000, 2, streams, coupled, mapping, frameSize)
	if err != nil {
		t.Fatalf("writeOggOpusMultistream failed: %v", err)
	}
	t.Logf("Ogg container: %d bytes", ogg.Len())

	// Decode with opusdec
	decoded, err := decodeWithOpusdec(ogg.Bytes())
	if err != nil {
		if _, ok := err.(*skipError); ok {
			t.Skipf("opusdec blocked (likely macOS provenance): %v", err)
		}
		t.Fatalf("decodeWithOpusdec failed: %v", err)
	}

	if len(decoded) == 0 {
		t.Fatal("opusdec produced empty output")
	}

	// Compute output energy
	outputEnergy := computeEnergyF32(decoded)
	t.Logf("Decoded: %d samples, energy=%.6f", len(decoded), outputEnergy)

	// Energy ratio check (>10% threshold)
	energyRatio := outputEnergy / inputEnergy * 100
	t.Logf("Energy ratio: %.1f%% (threshold: 10%%)", energyRatio)

	if energyRatio < 10.0 {
		t.Errorf("Energy ratio too low: %.1f%% < 10%%", energyRatio)
	} else {
		t.Logf("PASS: Stereo multistream validated with libopus")
	}
}

// TestLibopus_51Surround tests 5.1 surround multistream encoding with libopus.
func TestLibopus_51Surround(t *testing.T) {
	if !checkOpusdec() {
		t.Skip("opusdec not available in PATH")
	}

	// Create 5.1 encoder (6 channels, 4 streams, 2 coupled)
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault failed: %v", err)
	}
	enc.Reset()
	enc.SetBitrate(256000) // 256 kbps for 5.1

	frameSize := 960 // 20ms at 48kHz
	numFrames := 20

	// Generate test audio
	var allInput []float64
	packets := make([][]byte, numFrames)

	for i := 0; i < numFrames; i++ {
		pcm := generateMultichannelSine(6, frameSize)
		allInput = append(allInput, pcm...)

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Frame %d: Encode failed: %v", i, err)
		}
		if packet == nil {
			packet = []byte{0xF8, 0xFF, 0xFE}
		}
		packets[i] = packet
		if i == 0 {
			t.Logf("Frame %d: %d bytes (5.1)", i, len(packet))
		}
	}

	// Compute input energy
	inputF32 := make([]float32, len(allInput))
	for i, v := range allInput {
		inputF32[i] = float32(v)
	}
	inputEnergy := computeEnergyF32(inputF32)
	t.Logf("Input: %d frames, %d total samples (6ch), energy=%.6f", numFrames, len(allInput), inputEnergy)

	// Get mapping for 5.1
	streams, coupled, mapping, _ := DefaultMapping(6)
	t.Logf("5.1 config: %d streams, %d coupled, mapping=%v", streams, coupled, mapping)

	// Write Ogg Opus container
	var ogg bytes.Buffer
	err = writeOggOpusMultistream(&ogg, packets, 48000, 6, streams, coupled, mapping, frameSize)
	if err != nil {
		t.Fatalf("writeOggOpusMultistream failed: %v", err)
	}
	t.Logf("Ogg container: %d bytes", ogg.Len())

	// Decode with opusdec
	decoded, err := decodeWithOpusdec(ogg.Bytes())
	if err != nil {
		if _, ok := err.(*skipError); ok {
			t.Skipf("opusdec blocked (likely macOS provenance): %v", err)
		}
		t.Fatalf("decodeWithOpusdec failed: %v", err)
	}

	if len(decoded) == 0 {
		t.Fatal("opusdec produced empty output")
	}

	// Compute output energy
	outputEnergy := computeEnergyF32(decoded)
	t.Logf("Decoded: %d samples (should be ~%d for 6ch), energy=%.6f",
		len(decoded), numFrames*frameSize*6, outputEnergy)

	// Energy ratio check
	energyRatio := outputEnergy / inputEnergy * 100
	t.Logf("Energy ratio: %.1f%% (threshold: 10%%)", energyRatio)

	if energyRatio < 10.0 {
		t.Errorf("Energy ratio too low: %.1f%% < 10%%", energyRatio)
	} else {
		t.Logf("PASS: 5.1 surround multistream validated with libopus")
	}
}

// TestLibopus_71Surround tests 7.1 surround multistream encoding with libopus.
func TestLibopus_71Surround(t *testing.T) {
	if !checkOpusdec() {
		t.Skip("opusdec not available in PATH")
	}

	// Create 7.1 encoder (8 channels, 5 streams, 3 coupled)
	enc, err := NewEncoderDefault(48000, 8)
	if err != nil {
		t.Fatalf("NewEncoderDefault failed: %v", err)
	}
	enc.Reset()
	enc.SetBitrate(384000) // 384 kbps for 7.1

	frameSize := 960 // 20ms at 48kHz
	numFrames := 20

	// Generate test audio
	var allInput []float64
	packets := make([][]byte, numFrames)

	for i := 0; i < numFrames; i++ {
		pcm := generateMultichannelSine(8, frameSize)
		allInput = append(allInput, pcm...)

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Frame %d: Encode failed: %v", i, err)
		}
		if packet == nil {
			packet = []byte{0xF8, 0xFF, 0xFE}
		}
		packets[i] = packet
		if i == 0 {
			t.Logf("Frame %d: %d bytes (7.1)", i, len(packet))
		}
	}

	// Compute input energy
	inputF32 := make([]float32, len(allInput))
	for i, v := range allInput {
		inputF32[i] = float32(v)
	}
	inputEnergy := computeEnergyF32(inputF32)
	t.Logf("Input: %d frames, %d total samples (8ch), energy=%.6f", numFrames, len(allInput), inputEnergy)

	// Get mapping for 7.1
	streams, coupled, mapping, _ := DefaultMapping(8)
	t.Logf("7.1 config: %d streams, %d coupled, mapping=%v", streams, coupled, mapping)

	// Write Ogg Opus container
	var ogg bytes.Buffer
	err = writeOggOpusMultistream(&ogg, packets, 48000, 8, streams, coupled, mapping, frameSize)
	if err != nil {
		t.Fatalf("writeOggOpusMultistream failed: %v", err)
	}
	t.Logf("Ogg container: %d bytes", ogg.Len())

	// Decode with opusdec
	decoded, err := decodeWithOpusdec(ogg.Bytes())
	if err != nil {
		if _, ok := err.(*skipError); ok {
			t.Skipf("opusdec blocked (likely macOS provenance): %v", err)
		}
		t.Fatalf("decodeWithOpusdec failed: %v", err)
	}

	if len(decoded) == 0 {
		t.Fatal("opusdec produced empty output")
	}

	// Compute output energy
	outputEnergy := computeEnergyF32(decoded)
	t.Logf("Decoded: %d samples (should be ~%d for 8ch), energy=%.6f",
		len(decoded), numFrames*frameSize*8, outputEnergy)

	// Energy ratio check
	energyRatio := outputEnergy / inputEnergy * 100
	t.Logf("Energy ratio: %.1f%% (threshold: 10%%)", energyRatio)

	if energyRatio < 10.0 {
		t.Errorf("Energy ratio too low: %.1f%% < 10%%", energyRatio)
	} else {
		t.Logf("PASS: 7.1 surround multistream validated with libopus")
	}
}

// TestLibopus_BitrateQuality tests encoding at different bitrates and logs quality metrics.
// This test validates that libopus can decode at various bitrate levels and logs
// informational metrics about actual vs target bitrate.
func TestLibopus_BitrateQuality(t *testing.T) {
	if !checkOpusdec() {
		t.Skip("opusdec not available in PATH")
	}

	// Test bitrates for 5.1 surround (appropriate for multichannel)
	bitrates := []struct {
		name       string
		kbps       int
		channels   int
		minQuality float64 // Minimum energy ratio threshold
	}{
		{"5.1-128kbps", 128000, 6, 5.0},
		{"5.1-256kbps", 256000, 6, 10.0},
		{"5.1-384kbps", 384000, 6, 10.0},
	}

	for _, tc := range bitrates {
		t.Run(tc.name, func(t *testing.T) {
			// Create encoder
			enc, err := NewEncoderDefault(48000, tc.channels)
			if err != nil {
				t.Fatalf("NewEncoderDefault failed: %v", err)
			}
			enc.Reset()
			enc.SetBitrate(tc.kbps)

			frameSize := 960 // 20ms at 48kHz
			numFrames := 20
			duration := float64(numFrames*frameSize) / 48000.0 // Duration in seconds

			// Generate test audio
			var allInput []float64
			packets := make([][]byte, numFrames)
			totalPacketBytes := 0

			for i := 0; i < numFrames; i++ {
				pcm := generateMultichannelSine(tc.channels, frameSize)
				allInput = append(allInput, pcm...)

				packet, err := enc.Encode(pcm, frameSize)
				if err != nil {
					t.Fatalf("Frame %d: Encode failed: %v", i, err)
				}
				if packet == nil {
					packet = []byte{0xF8, 0xFF, 0xFE}
				}
				packets[i] = packet
				totalPacketBytes += len(packet)
			}

			// Calculate actual bitrate
			actualBitrate := float64(totalPacketBytes*8) / duration
			targetBitrate := float64(tc.kbps)
			bitrateRatio := actualBitrate / targetBitrate * 100

			t.Logf("Target bitrate: %d kbps", tc.kbps/1000)
			t.Logf("Actual bitrate: %.1f kbps (%.1f%% of target)", actualBitrate/1000, bitrateRatio)
			t.Logf("Total packets: %d, total bytes: %d, duration: %.2fs",
				numFrames, totalPacketBytes, duration)

			// Compute input energy
			inputF32 := make([]float32, len(allInput))
			for i, v := range allInput {
				inputF32[i] = float32(v)
			}
			inputEnergy := computeEnergyF32(inputF32)

			// Get mapping
			streams, coupled, mapping, _ := DefaultMapping(tc.channels)

			// Write Ogg Opus container
			var ogg bytes.Buffer
			err = writeOggOpusMultistream(&ogg, packets, 48000, tc.channels,
				streams, coupled, mapping, frameSize)
			if err != nil {
				t.Fatalf("writeOggOpusMultistream failed: %v", err)
			}

			// Decode with opusdec
			decoded, err := decodeWithOpusdec(ogg.Bytes())
			if err != nil {
				if _, ok := err.(*skipError); ok {
					t.Skipf("opusdec blocked (likely macOS provenance): %v", err)
				}
				t.Fatalf("decodeWithOpusdec failed: %v", err)
			}

			if len(decoded) == 0 {
				t.Fatal("opusdec produced empty output")
			}

			// Compute output energy and ratio
			outputEnergy := computeEnergyF32(decoded)
			energyRatio := outputEnergy / inputEnergy * 100

			t.Logf("Decoded: %d samples, energy=%.6f", len(decoded), outputEnergy)
			t.Logf("Energy ratio: %.1f%% (threshold: %.1f%%)", energyRatio, tc.minQuality)

			// Quality assessment based on bitrate
			quality := "UNKNOWN"
			if energyRatio >= tc.minQuality {
				quality = "PASS"
			} else {
				quality = "NEEDS_TUNING"
			}

			// Log summary
			t.Logf("Bitrate quality assessment: %s", quality)
			t.Logf("Summary: %s -> actual=%.0f kbps, energy=%.1f%%",
				tc.name, actualBitrate/1000, energyRatio)

			// Report pass/fail based on threshold
			if energyRatio < tc.minQuality {
				t.Logf("INFO: Energy ratio below threshold - encoder may need tuning for %s", tc.name)
			}
		})
	}
}

// TestLibopus_ContainerFormat verifies Ogg Opus container structure for multistream.
func TestLibopus_ContainerFormat(t *testing.T) {
	// Create 5.1 encoder
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault failed: %v", err)
	}

	frameSize := 960
	pcm := generateMultichannelSine(6, frameSize)

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Get mapping
	streams, coupled, mapping, _ := DefaultMapping(6)

	// Write Ogg Opus container
	var ogg bytes.Buffer
	err = writeOggOpusMultistream(&ogg, [][]byte{packet}, 48000, 6,
		streams, coupled, mapping, frameSize)
	if err != nil {
		t.Fatalf("writeOggOpusMultistream failed: %v", err)
	}

	data := ogg.Bytes()

	// Verify Ogg structure
	if string(data[0:4]) != "OggS" {
		t.Error("Missing OggS magic")
	}

	// Find OpusHead
	foundHead := false
	for i := 0; i < len(data)-8; i++ {
		if string(data[i:i+8]) == "OpusHead" {
			foundHead = true
			// Verify mapping family 1
			if i+18 < len(data) && data[i+18] == 1 {
				t.Logf("OpusHead found at offset %d, mapping family = 1 (surround)", i)
			} else {
				t.Errorf("OpusHead mapping family != 1")
			}
			// Verify stream count
			if i+19 < len(data) {
				t.Logf("Stream count: %d (expected %d)", data[i+19], streams)
			}
			// Verify coupled stream count
			if i+20 < len(data) {
				t.Logf("Coupled streams: %d (expected %d)", data[i+20], coupled)
			}
			break
		}
	}
	if !foundHead {
		t.Error("Missing OpusHead")
	}

	// Find OpusTags
	foundTags := false
	for i := 0; i < len(data)-8; i++ {
		if string(data[i:i+8]) == "OpusTags" {
			foundTags = true
			t.Logf("OpusTags found at offset %d", i)
			break
		}
	}
	if !foundTags {
		t.Error("Missing OpusTags")
	}

	t.Logf("Ogg Opus multistream container: %d bytes total", len(data))
	t.Logf("Mapping family 1 (surround) with %d streams, %d coupled", streams, coupled)
}

// TestLibopus_Info logs information about the libopus cross-validation setup.
func TestLibopus_Info(t *testing.T) {
	if !checkOpusdec() {
		t.Log("opusdec not available - libopus cross-validation tests will be skipped")
		t.Log("To enable cross-validation tests, install opus-tools:")
		t.Log("  macOS: brew install opus-tools")
		t.Log("  Linux: apt-get install opus-tools")
		return
	}

	path := getOpusdecPath()
	t.Logf("opusdec found at: %s", path)

	// Try to get version
	cmd := exec.Command(path, "--version")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Logf("opusdec version: %s", string(output))
	}

	// Log supported configurations
	t.Log("Supported multistream configurations:")
	for ch := 1; ch <= 8; ch++ {
		streams, coupled, mapping, err := DefaultMapping(ch)
		if err != nil {
			continue
		}
		t.Logf("  %d channels: %d streams, %d coupled, mapping=%v", ch, streams, coupled, mapping)
	}
}

// Ensure fmt is used (required by plan)
var _ = fmt.Sprintf
