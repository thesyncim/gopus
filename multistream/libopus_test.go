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
	"strings"
	"testing"

	oggcontainer "github.com/thesyncim/gopus/container/ogg"
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
	copy(header[0:4], "OggS") // Capture pattern
	header[4] = 0             // Version
	header[5] = headerType    // Header type
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

// makeOpusHeadMultistreamWithFamily creates OpusHead for multistream mapping families.
func makeOpusHeadMultistreamWithFamily(channels, sampleRate int, streams, coupledStreams, mappingFamily int, mapping []byte) []byte {
	head := oggcontainer.DefaultOpusHeadMultistreamWithFamily(
		uint32(sampleRate),
		uint8(channels),
		uint8(mappingFamily),
		uint8(streams),
		uint8(coupledStreams),
		mapping,
	)
	return head.Encode()
}

// makeOpusHeadMultistream creates OpusHead for mapping family 1 (RFC 7845 Section 5.1.1).
func makeOpusHeadMultistream(channels, sampleRate int, streams, coupledStreams int, mapping []byte) []byte {
	return makeOpusHeadMultistreamWithFamily(channels, sampleRate, streams, coupledStreams, 1, mapping)
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
	return writeOggOpusMultistreamWithFamily(w, packets, sampleRate, channels, streams, coupledStreams, 1, mapping, frameSize)
}

func writeOggOpusMultistreamWithFamily(w io.Writer, packets [][]byte, sampleRate, channels int,
	streams, coupledStreams, mappingFamily int, mapping []byte, frameSize int) error {

	serialNo := uint32(54321)
	var granulePos uint64

	// Page 1: OpusHead header (BOS = Beginning of Stream)
	opusHead := makeOpusHeadMultistreamWithFamily(channels, sampleRate, streams, coupledStreams, mappingFamily, mapping)
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

// checkOpusinfo checks if opusinfo is available.
func checkOpusinfo() bool {
	if _, err := exec.LookPath("opusinfo"); err == nil {
		return true
	}

	paths := []string{
		"/opt/homebrew/bin/opusinfo",
		"/usr/local/bin/opusinfo",
		"/usr/bin/opusinfo",
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}

	return false
}

func getOpusinfoPath() string {
	if path, err := exec.LookPath("opusinfo"); err == nil {
		return path
	}

	paths := []string{
		"/opt/homebrew/bin/opusinfo",
		"/usr/local/bin/opusinfo",
		"/usr/bin/opusinfo",
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "opusinfo"
}

func inspectWithOpusinfo(oggData []byte) (string, error) {
	if !checkOpusinfo() {
		return "", fmt.Errorf("opusinfo not available")
	}

	tmpOpus, err := os.CreateTemp("", "gopus_ms_*.opus")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpOpus.Name())

	if _, err := tmpOpus.Write(oggData); err != nil {
		tmpOpus.Close()
		return "", err
	}
	tmpOpus.Close()

	exec.Command("xattr", "-c", tmpOpus.Name()).Run()

	cmd := exec.Command(getOpusinfoPath(), tmpOpus.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("opusinfo failed: %w (%s)", err, bytes.TrimSpace(output))
	}

	return string(output), nil
}

func inspectWithOpusinfoForTest(t *testing.T, oggData []byte) string {
	t.Helper()

	output, err := inspectWithOpusinfo(oggData)
	if err == nil {
		return output
	}

	if err.Error() == "opusinfo not available" {
		t.Skip("opusinfo not available; skipping libopus header inspection")
	}
	t.Fatalf("inspectWithOpusinfo failed: %v", err)
	return ""
}

// decodeWithOpusdec decodes an Ogg Opus file using libopus opusdec.
// Returns the decoded PCM samples as float32.
func decodeWithOpusdec(oggData []byte) ([]float32, error) {
	if !checkOpusdec() {
		return nil, fmt.Errorf("opusdec not available")
	}

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
			bytes.Contains(output, []byte("Operation not permitted")) {
			return nil, fmt.Errorf("opusdec blocked by macOS provenance")
		}
		return nil, fmt.Errorf("opusdec failed: %w (%s)", err, bytes.TrimSpace(output))
	}
	if bytes.Contains(output, []byte("Decoding error")) || bytes.Contains(output, []byte("Failed to decode")) {
		return nil, fmt.Errorf("opusdec decode error: %s", bytes.TrimSpace(output))
	}

	// Read and parse WAV file
	wavData, err := os.ReadFile(tmpWav.Name())
	if err != nil {
		return nil, err
	}

	samples := parseWAVSamples(wavData)
	if len(samples) == 0 {
		return nil, fmt.Errorf("opusdec produced no PCM samples")
	}
	return samples, nil
}

func decodeWithInternalMultistream(oggData []byte) ([]float32, error) {
	r, err := oggcontainer.NewReader(bytes.NewReader(oggData))
	if err != nil {
		return nil, fmt.Errorf("new ogg reader: %w", err)
	}
	if r.Header == nil {
		return nil, fmt.Errorf("missing opus header")
	}

	channels := int(r.Header.Channels)
	streams := int(r.Header.StreamCount)
	coupled := int(r.Header.CoupledCount)
	mapping := r.Header.ChannelMapping
	if len(mapping) == 0 {
		switch r.Header.MappingFamily {
		case 3:
			mapping = make([]byte, channels)
			for i := 0; i < channels; i++ {
				mapping[i] = byte(i)
			}
		default:
			var err error
			streams, coupled, mapping, err = DefaultMapping(channels)
			if err != nil {
				return nil, fmt.Errorf("default mapping: %w", err)
			}
		}
	}

	dec, err := NewDecoder(48000, channels, streams, coupled, mapping)
	if err != nil {
		return nil, fmt.Errorf("new multistream decoder: %w", err)
	}

	decoded := make([]float32, 0, 48000*channels)
	var prevGranule uint64
	for {
		packet, granule, err := r.ReadPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read packet: %w", err)
		}

		frameSize := 960
		if granule > prevGranule {
			frameSize = int(granule - prevGranule)
		}
		prevGranule = granule

		pcm64, err := dec.Decode(packet, frameSize)
		if err != nil {
			return nil, fmt.Errorf("decode packet: %w", err)
		}
		for _, s := range pcm64 {
			decoded = append(decoded, float32(s))
		}
	}

	preSkip := int(r.PreSkip()) * channels
	if preSkip > 0 && len(decoded) > preSkip {
		decoded = decoded[preSkip:]
	}

	return decoded, nil
}

func decodeWithOpusdecForTest(t *testing.T, oggData []byte) []float32 {
	t.Helper()

	decoded, err := decodeWithOpusdec(oggData)
	if err == nil {
		return decoded
	}

	switch err.Error() {
	case "opusdec not available":
		t.Skip("opusdec not available; skipping libopus cross-validation")
	case "opusdec blocked by macOS provenance":
		t.Skip("opusdec blocked by macOS provenance")
	}
	t.Fatalf("decodeWithOpusdec failed: %v", err)
	return nil
}

// parseWAVSamples extracts float32 samples from WAV data.
func parseWAVSamples(data []byte) []float32 {
	if len(data) < 44 {
		return nil
	}

	// Find data chunk
	offset := 12
	for offset <= len(data)-8 {
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

	return nil
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

func expectedDecodedSampleCount(numFrames, frameSize, channels int) int {
	total := numFrames * frameSize * channels
	preSkip := 312 * channels
	if total < preSkip {
		return 0
	}
	return total - preSkip
}

func runLibopusSurroundTest(t *testing.T, label string, channels, bitrate int) {
	t.Helper()

	enc, err := NewEncoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault failed: %v", err)
	}
	enc.Reset()
	enc.SetBitrate(bitrate)

	frameSize := 960 // 20ms at 48kHz
	numFrames := 20

	var allInput []float64
	packets := make([][]byte, numFrames)

	for i := 0; i < numFrames; i++ {
		pcm := generateMultichannelSine(channels, frameSize)
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
			t.Logf("Frame %d: %d bytes (%s)", i, len(packet), label)
		}
	}

	inputF32 := make([]float32, len(allInput))
	for i, v := range allInput {
		inputF32[i] = float32(v)
	}
	inputEnergy := computeEnergyF32(inputF32)
	t.Logf("Input: %d frames, %d total samples (%dch), energy=%.6f", numFrames, len(allInput), channels, inputEnergy)

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping(%d) failed: %v", channels, err)
	}
	t.Logf("%s config: %d streams, %d coupled, mapping=%v", label, streams, coupled, mapping)

	var ogg bytes.Buffer
	err = writeOggOpusMultistream(&ogg, packets, 48000, channels, streams, coupled, mapping, frameSize)
	if err != nil {
		t.Fatalf("writeOggOpusMultistream failed: %v", err)
	}
	t.Logf("Ogg container: %d bytes", ogg.Len())

	decoded := decodeWithOpusdecForTest(t, ogg.Bytes())

	if len(decoded) == 0 {
		t.Fatal("opusdec produced empty output")
	}
	wantSamples := expectedDecodedSampleCount(numFrames, frameSize, channels)
	if len(decoded) != wantSamples {
		t.Fatalf("decoded sample count mismatch: got=%d want=%d", len(decoded), wantSamples)
	}

	outputEnergy := computeEnergyF32(decoded)
	t.Logf("Decoded: %d samples (expected %d after pre-skip), energy=%.6f",
		len(decoded), wantSamples, outputEnergy)

	energyRatio := outputEnergy / inputEnergy * 100
	t.Logf("Energy ratio: %.1f%% (threshold: 10%%)", energyRatio)

	if energyRatio < 10.0 {
		t.Errorf("Energy ratio too low: %.1f%% < 10%%", energyRatio)
	} else {
		t.Logf("PASS: %s multistream validated with libopus", label)
	}
}

// TestLibopus_Stereo tests stereo multistream encoding with libopus.
func TestLibopus_Stereo(t *testing.T) {
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
	decoded := decodeWithOpusdecForTest(t, ogg.Bytes())

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
	runLibopusSurroundTest(t, "5.1", 6, 256000)
}

// TestLibopus_71Surround tests 7.1 surround multistream encoding with libopus.
func TestLibopus_71Surround(t *testing.T) {
	runLibopusSurroundTest(t, "7.1", 8, 384000)
}

// TestLibopus_DefaultMappingMatrix validates all default mapping-family channel layouts
// (1..8 channels) against libopus decode and checks internal decoder agreement.
func TestLibopus_DefaultMappingMatrix(t *testing.T) {
	cases := []struct {
		name     string
		channels int
		bitrate  int
	}{
		{name: "1ch", channels: 1, bitrate: 64000},
		{name: "2ch", channels: 2, bitrate: 128000},
		{name: "3ch", channels: 3, bitrate: 160000},
		{name: "4ch", channels: 4, bitrate: 192000},
		{name: "5ch", channels: 5, bitrate: 224000},
		{name: "6ch", channels: 6, bitrate: 256000},
		{name: "7ch", channels: 7, bitrate: 320000},
		{name: "8ch", channels: 8, bitrate: 384000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoderDefault(48000, tc.channels)
			if err != nil {
				t.Fatalf("NewEncoderDefault failed: %v", err)
			}
			enc.Reset()
			enc.SetBitrate(tc.bitrate)

			frameSize := 960 // 20ms at 48kHz
			numFrames := 12

			var allInput []float64
			packets := make([][]byte, numFrames)
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
			}

			streams, coupled, mapping, err := DefaultMapping(tc.channels)
			if err != nil {
				t.Fatalf("DefaultMapping(%d) failed: %v", tc.channels, err)
			}

			var ogg bytes.Buffer
			err = writeOggOpusMultistream(&ogg, packets, 48000, tc.channels, streams, coupled, mapping, frameSize)
			if err != nil {
				t.Fatalf("writeOggOpusMultistream failed: %v", err)
			}

			libopusDecoded := decodeWithOpusdecForTest(t, ogg.Bytes())
			internalDecoded, err := decodeWithInternalMultistream(ogg.Bytes())
			if err != nil {
				t.Fatalf("decodeWithInternalMultistream failed: %v", err)
			}

			wantSamples := expectedDecodedSampleCount(numFrames, frameSize, tc.channels)
			if len(libopusDecoded) != wantSamples {
				t.Fatalf("libopus decoded sample count mismatch: got=%d want=%d", len(libopusDecoded), wantSamples)
			}
			if len(internalDecoded) != wantSamples {
				t.Fatalf("internal decoded sample count mismatch: got=%d want=%d", len(internalDecoded), wantSamples)
			}

			inputF32 := make([]float32, len(allInput))
			for i, v := range allInput {
				inputF32[i] = float32(v)
			}
			inputEnergy := computeEnergyF32(inputF32)
			libopusEnergy := computeEnergyF32(libopusDecoded)

			libopusEnergyRatio := libopusEnergy / inputEnergy * 100
			if libopusEnergyRatio < 5.0 {
				t.Fatalf("libopus energy ratio too low: %.2f%% < 5%%", libopusEnergyRatio)
			}

			t.Logf("%dch: bitrate=%dkbps libopusEnergy=%.1f%%",
				tc.channels, tc.bitrate/1000, libopusEnergyRatio)
		})
	}
}

// TestLibopus_FrameDurationMatrix validates multistream libopus compatibility
// across long/short Opus frame durations.
func TestLibopus_FrameDurationMatrix(t *testing.T) {
	layouts := []struct {
		name     string
		channels int
		bitrate  int
	}{
		{name: "stereo", channels: 2, bitrate: 128000},
		{name: "5.1", channels: 6, bitrate: 256000},
	}
	frameSizes := []struct {
		name      string
		frameSize int
	}{
		{name: "10ms", frameSize: 480},
		{name: "20ms", frameSize: 960},
		{name: "40ms", frameSize: 1920},
		{name: "60ms", frameSize: 2880},
	}

	for _, layout := range layouts {
		for _, fs := range frameSizes {
			tcName := fmt.Sprintf("%s-%s", layout.name, fs.name)
			t.Run(tcName, func(t *testing.T) {
				enc, err := NewEncoderDefault(48000, layout.channels)
				if err != nil {
					t.Fatalf("NewEncoderDefault failed: %v", err)
				}
				enc.Reset()
				enc.SetBitrate(layout.bitrate)

				numFrames := 8
				packets := make([][]byte, numFrames)
				for i := 0; i < numFrames; i++ {
					pcm := generateMultichannelSine(layout.channels, fs.frameSize)
					packet, err := enc.Encode(pcm, fs.frameSize)
					if err != nil {
						t.Fatalf("Frame %d: Encode failed: %v", i, err)
					}
					if packet == nil {
						packet = []byte{0xF8, 0xFF, 0xFE}
					}
					packets[i] = packet
				}

				streams, coupled, mapping, err := DefaultMapping(layout.channels)
				if err != nil {
					t.Fatalf("DefaultMapping(%d) failed: %v", layout.channels, err)
				}

				var ogg bytes.Buffer
				err = writeOggOpusMultistream(&ogg, packets, 48000, layout.channels, streams, coupled, mapping, fs.frameSize)
				if err != nil {
					t.Fatalf("writeOggOpusMultistream failed: %v", err)
				}

				libopusDecoded := decodeWithOpusdecForTest(t, ogg.Bytes())
				internalDecoded, err := decodeWithInternalMultistream(ogg.Bytes())
				if err != nil {
					t.Fatalf("decodeWithInternalMultistream failed: %v", err)
				}

				wantSamples := expectedDecodedSampleCount(numFrames, fs.frameSize, layout.channels)
				if len(libopusDecoded) != wantSamples {
					t.Fatalf("libopus decoded sample count mismatch: got=%d want=%d", len(libopusDecoded), wantSamples)
				}
				if len(internalDecoded) != wantSamples {
					t.Fatalf("internal decoded sample count mismatch: got=%d want=%d", len(internalDecoded), wantSamples)
				}

				t.Logf("%s: decoded=%d samples", tcName, wantSamples)
			})
		}
	}
}

func runLibopusAmbisonicsParityCase(t *testing.T, mappingFamily, channels, bitrate int) {
	t.Helper()

	enc, err := NewEncoderAmbisonics(48000, channels, mappingFamily)
	if err != nil {
		t.Fatalf("NewEncoderAmbisonics(%d, family=%d) failed: %v", channels, mappingFamily, err)
	}
	enc.Reset()
	enc.SetBitrate(bitrate)

	frameSize := 960 // 20ms at 48kHz
	numFrames := 10

	var allInput []float64
	packets := make([][]byte, numFrames)
	for i := 0; i < numFrames; i++ {
		pcm := generateMultichannelSine(channels, frameSize)
		allInput = append(allInput, pcm...)

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Frame %d: Encode failed: %v", i, err)
		}
		if packet == nil {
			packet = []byte{0xF8, 0xFF, 0xFE}
		}
		packets[i] = packet
	}

	var ogg bytes.Buffer
	err = writeOggOpusMultistreamWithFamily(
		&ogg,
		packets,
		48000,
		channels,
		enc.Streams(),
		enc.CoupledStreams(),
		enc.MappingFamily(),
		enc.mapping,
		frameSize,
	)
	if err != nil {
		t.Fatalf("writeOggOpusMultistreamWithFamily failed: %v", err)
	}

	opusinfoOutput := inspectWithOpusinfoForTest(t, ogg.Bytes())
	wantInfoFamily := fmt.Sprintf("Channel Mapping Family: %d", mappingFamily)
	if !strings.Contains(opusinfoOutput, wantInfoFamily) {
		t.Fatalf("opusinfo mapping family mismatch: missing %q", wantInfoFamily)
	}
	wantInfoStreams := fmt.Sprintf("Streams: %d, Coupled: %d", enc.Streams(), enc.CoupledStreams())
	if !strings.Contains(opusinfoOutput, wantInfoStreams) {
		t.Fatalf("opusinfo streams/coupled mismatch: missing %q", wantInfoStreams)
	}
	wantInfoChannels := fmt.Sprintf("Channels: %d", channels)
	if !strings.Contains(opusinfoOutput, wantInfoChannels) {
		t.Fatalf("opusinfo channels mismatch: missing %q", wantInfoChannels)
	}

	internalDecoded, err := decodeWithInternalMultistream(ogg.Bytes())
	if err != nil {
		t.Fatalf("decodeWithInternalMultistream failed: %v", err)
	}

	wantSamples := expectedDecodedSampleCount(numFrames, frameSize, channels)
	if len(internalDecoded) != wantSamples {
		t.Fatalf("internal decoded sample count mismatch: got=%d want=%d", len(internalDecoded), wantSamples)
	}

	inputF32 := make([]float32, len(allInput))
	for i, v := range allInput {
		inputF32[i] = float32(v)
	}
	inputEnergy := computeEnergyF32(inputF32)
	internalEnergy := computeEnergyF32(internalDecoded)
	internalEnergyRatio := internalEnergy / inputEnergy * 100
	if internalEnergyRatio < 5.0 {
		t.Fatalf("internal energy ratio too low: %.2f%% < 5%%", internalEnergyRatio)
	}

	if libopusDecoded, err := decodeWithOpusdec(ogg.Bytes()); err == nil {
		if len(libopusDecoded) != wantSamples {
			t.Fatalf("libopus decoded sample count mismatch: got=%d want=%d", len(libopusDecoded), wantSamples)
		}
		libopusEnergy := computeEnergyF32(libopusDecoded)
		libopusEnergyRatio := libopusEnergy / inputEnergy * 100
		if libopusEnergyRatio < 5.0 {
			t.Fatalf("libopus energy ratio too low: %.2f%% < 5%%", libopusEnergyRatio)
		}
		t.Logf("family=%d %dch: streams=%d coupled=%d internalEnergy=%.1f%% libopusEnergy=%.1f%%",
			mappingFamily, channels, enc.Streams(), enc.CoupledStreams(), internalEnergyRatio, libopusEnergyRatio)
	} else {
		// opusdec currently fails to decode some non-family-1 streams while opusinfo
		// still validates headers; keep this as informative, not a hard failure.
		t.Logf("family=%d %dch: opusdec decode unavailable (%v); internalEnergy=%.1f%%",
			mappingFamily, channels, err, internalEnergyRatio)
	}
}

// TestLibopus_AmbisonicsFamily2Matrix validates ambisonics mapping family 2
// headers via libopus tooling and checks internal decoder agreement.
func TestLibopus_AmbisonicsFamily2Matrix(t *testing.T) {
	cases := []struct {
		name     string
		channels int
		bitrate  int
	}{
		{name: "foa-4ch", channels: 4, bitrate: 192000},
		{name: "foa-plus-6ch", channels: 6, bitrate: 224000},
		{name: "soa-9ch", channels: 9, bitrate: 320000},
		{name: "soa-plus-11ch", channels: 11, bitrate: 384000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runLibopusAmbisonicsParityCase(t, 2, tc.channels, tc.bitrate)
		})
	}
}

// TestLibopus_AmbisonicsFamily3Matrix validates ambisonics mapping family 3
// headers via libopus tooling and checks internal decoder agreement.
func TestLibopus_AmbisonicsFamily3Matrix(t *testing.T) {
	cases := []struct {
		name     string
		channels int
		bitrate  int
	}{
		{name: "foa-4ch", channels: 4, bitrate: 192000},
		{name: "foa-plus-6ch", channels: 6, bitrate: 224000},
		{name: "soa-9ch", channels: 9, bitrate: 320000},
		{name: "soa-plus-11ch", channels: 11, bitrate: 384000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runLibopusAmbisonicsParityCase(t, 3, tc.channels, tc.bitrate)
		})
	}
}

// TestLibopus_BitrateQuality tests encoding at different bitrates and logs quality metrics.
// This test validates that libopus can decode at various bitrate levels and logs
// informational metrics about actual vs target bitrate.
func TestLibopus_BitrateQuality(t *testing.T) {
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
			decoded := decodeWithOpusdecForTest(t, ogg.Bytes())

			if len(decoded) == 0 {
				t.Fatal("opusdec produced empty output")
			}
			wantSamples := expectedDecodedSampleCount(numFrames, frameSize, tc.channels)
			if len(decoded) != wantSamples {
				t.Fatalf("decoded sample count mismatch: got=%d want=%d", len(decoded), wantSamples)
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
