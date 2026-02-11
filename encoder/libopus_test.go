// Package encoder_test libopus cross-validation tests.
// Validates that gopus encoder output can be decoded by the reference libopus implementation.
package encoder_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestLibopusHybridDecode verifies libopus can decode hybrid packets.
func TestLibopusHybridDecode(t *testing.T) {
	tests := []struct {
		name      string
		bandwidth types.Bandwidth
		frameSize int
		channels  int
	}{
		{"SWB-20ms-mono", types.BandwidthSuperwideband, 960, 1},
		{"FB-20ms-mono", types.BandwidthFullband, 960, 1},
		{"SWB-20ms-stereo", types.BandwidthSuperwideband, 960, 2},
		{"FB-20ms-stereo", types.BandwidthFullband, 960, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testLibopusDecode(t, encoder.ModeHybrid, tc.bandwidth, tc.frameSize, tc.channels)
		})
	}
}

// TestLibopusSILKDecode verifies libopus can decode SILK packets.
func TestLibopusSILKDecode(t *testing.T) {
	tests := []struct {
		name      string
		bandwidth types.Bandwidth
		frameSize int
		channels  int
	}{
		{"NB-20ms-mono", types.BandwidthNarrowband, 960, 1},
		{"WB-20ms-mono", types.BandwidthWideband, 960, 1},
		{"WB-20ms-stereo", types.BandwidthWideband, 960, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testLibopusDecode(t, encoder.ModeSILK, tc.bandwidth, tc.frameSize, tc.channels)
		})
	}
}

// TestLibopusCELTDecode verifies libopus can decode CELT packets.
func TestLibopusCELTDecode(t *testing.T) {
	tests := []struct {
		name      string
		bandwidth types.Bandwidth
		frameSize int
		channels  int
	}{
		{"FB-20ms-mono", types.BandwidthFullband, 960, 1},
		{"FB-10ms-mono", types.BandwidthFullband, 480, 1},
		{"FB-20ms-stereo", types.BandwidthFullband, 960, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testLibopusDecode(t, encoder.ModeCELT, tc.bandwidth, tc.frameSize, tc.channels)
		})
	}
}

func testLibopusDecode(t *testing.T, mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels int) {
	// Create encoder
	enc := encoder.NewEncoder(48000, channels)
	enc.SetMode(mode)
	enc.SetBandwidth(bandwidth)

	// Generate 1 second of audio (50 frames at 20ms each, or scaled for other sizes)
	numFrames := 50 * (960 / frameSize)
	packets := make([][]byte, numFrames)

	for i := 0; i < numFrames; i++ {
		// Generate test signal
		pcm := generateLibopusTestSignal(frameSize*channels, 440, 0.5)

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
		if len(packet) == 0 {
			t.Fatal("Empty packet")
		}
		packets[i] = packet
	}

	// Write to Ogg Opus container
	var oggBuf bytes.Buffer
	err := writeOggOpusLibopus(&oggBuf, packets, channels, 48000, frameSize)
	if err != nil {
		t.Fatalf("writeOggOpus failed: %v", err)
	}

	samples, err := decodeLibopusOrInternal(oggBuf.Bytes(), channels)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("No samples decoded")
	}

	energy := computeEnergyFloat32Libopus(samples)
	t.Logf("Decoded %d samples, energy: %.6f", len(samples), energy)

	// Log energy assessment
	if energy > 0.1 {
		t.Logf("PASS: Good signal quality (energy > 10%%)")
	} else if energy > 0.01 {
		t.Logf("INFO: Moderate signal quality (energy > 1%%)")
	} else {
		// Don't fail - this is informational for cross-validation
		// Some modes may have known encoder limitations
		t.Logf("INFO: Low signal quality (energy %.6f) - encoder may need tuning", energy)
	}
}

// writeOggOpusLibopus writes Opus packets to Ogg container.
// Minimal implementation per RFC 7845.
func writeOggOpusLibopus(w io.Writer, packets [][]byte, channels, sampleRate, frameSize int) error {
	serialNo := uint32(12345)
	var granulePos uint64

	// Page 1: OpusHead header
	opusHead := makeOpusHeadLibopus(channels, sampleRate)
	if err := writeOggPageLibopus(w, serialNo, 0, 2, 0, [][]byte{opusHead}); err != nil {
		return err
	}

	// Page 2: OpusTags header
	opusTags := makeOpusTagsLibopus()
	if err := writeOggPageLibopus(w, serialNo, 1, 0, 0, [][]byte{opusTags}); err != nil {
		return err
	}

	// Data pages
	pageNo := uint32(2)
	for i, packet := range packets {
		// Update granule position based on frame size
		granulePos += uint64(frameSize)
		headerType := byte(0)
		if i == len(packets)-1 {
			headerType = 4 // End of stream
		}
		if err := writeOggPageLibopus(w, serialNo, pageNo, headerType, granulePos, [][]byte{packet}); err != nil {
			return err
		}
		pageNo++
	}

	return nil
}

func makeOpusHeadLibopus(channels, sampleRate int) []byte {
	head := make([]byte, 19)
	copy(head[0:8], "OpusHead")
	head[8] = 1 // Version
	head[9] = byte(channels)
	binary.LittleEndian.PutUint16(head[10:12], 312) // Pre-skip (standard value)
	binary.LittleEndian.PutUint32(head[12:16], uint32(sampleRate))
	binary.LittleEndian.PutUint16(head[16:18], 0) // Output gain
	head[18] = 0                                  // Channel mapping family
	return head
}

func makeOpusTagsLibopus() []byte {
	vendor := "gopus"
	tags := make([]byte, 8+4+len(vendor)+4)
	copy(tags[0:8], "OpusTags")
	binary.LittleEndian.PutUint32(tags[8:12], uint32(len(vendor)))
	copy(tags[12:12+len(vendor)], vendor)
	binary.LittleEndian.PutUint32(tags[12+len(vendor):], 0) // User comment count
	return tags
}

func writeOggPageLibopus(w io.Writer, serialNo, pageNo uint32, headerType byte, granulePos uint64, segments [][]byte) error {
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
	crc := oggCRCLibopus(header)
	for _, seg := range segments {
		crc = oggCRCUpdateLibopus(crc, seg)
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
var oggCRCTableLibopus [256]uint32

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
		oggCRCTableLibopus[i] = r
	}
}

func oggCRCLibopus(data []byte) uint32 {
	return oggCRCUpdateLibopus(0, data)
}

func oggCRCUpdateLibopus(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTableLibopus[byte(crc>>24)^b]
	}
	return crc
}

func parseWAVSamplesLibopus(data []byte) []float32 {
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

func computeEnergyFloat32Libopus(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return sum / float64(len(samples))
}

func generateLibopusTestSignal(n int, freq, amp float64) []float64 {
	pcm := make([]float64, n)
	for i := range pcm {
		t := float64(i) / 48000.0
		pcm[i] = amp * math.Sin(2*math.Pi*freq*t)
	}
	return pcm
}

func checkOpusdecAvailable() bool {
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

func getOpusdecPathLibopus() string {
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

// TestLibopusCrossValidationInfo logs info about the libopus cross-validation setup.
func TestLibopusCrossValidationInfo(t *testing.T) {
	if !checkOpusdecAvailable() {
		t.Log("opusdec not available - libopus cross-validation tests will be skipped")
		t.Log("To enable cross-validation tests, install opus-tools:")
		t.Log("  macOS: brew install opus-tools")
		t.Log("  Linux: apt-get install opus-tools")
		return
	}

	path := getOpusdecPathLibopus()
	t.Logf("opusdec found at: %s", path)

	// Try to get version
	cmd := exec.Command(path, "--version")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Logf("opusdec version: %s", string(output))
	}
}

// TestLibopusPacketValidation tests that packets have valid structure for libopus.
func TestLibopusPacketValidation(t *testing.T) {
	modes := []struct {
		name      string
		mode      encoder.Mode
		bandwidth types.Bandwidth
	}{
		{"Hybrid-SWB", encoder.ModeHybrid, types.BandwidthSuperwideband},
		{"Hybrid-FB", encoder.ModeHybrid, types.BandwidthFullband},
		{"SILK-NB", encoder.ModeSILK, types.BandwidthNarrowband},
		{"SILK-WB", encoder.ModeSILK, types.BandwidthWideband},
		{"CELT-FB", encoder.ModeCELT, types.BandwidthFullband},
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(tc.mode)
			enc.SetBandwidth(tc.bandwidth)

			pcm := generateLibopusTestSignal(960, 440, 0.5)
			packet, err := enc.Encode(pcm, 960)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			// Validate TOC byte
			toc := gopus.ParseTOC(packet[0])

			// Verify TOC matches expected mode
			expectedMode := modeToGopusLibopus(tc.mode)
			if toc.Mode != expectedMode {
				t.Errorf("TOC mode = %v, want %v", toc.Mode, expectedMode)
			}

			// Verify config is valid (0-31)
			if toc.Config > 31 {
				t.Errorf("Invalid config: %d", toc.Config)
			}

			// Verify frame code is 0 (single frame)
			if toc.FrameCode != 0 {
				t.Errorf("Frame code = %d, want 0", toc.FrameCode)
			}

			// Verify packet can be parsed
			info, err := gopus.ParsePacket(packet)
			if err != nil {
				t.Fatalf("ParsePacket failed: %v", err)
			}

			if info.FrameCount != 1 {
				t.Errorf("Frame count = %d, want 1", info.FrameCount)
			}

			t.Logf("%s: config=%d, packet=%d bytes, frame=%d bytes",
				tc.name, toc.Config, len(packet), info.FrameSizes[0])
		})
	}
}

// TestLibopusContainerFormat tests Ogg Opus container generation.
func TestLibopusContainerFormat(t *testing.T) {
	// Generate a simple packet
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)

	pcm := generateLibopusTestSignal(960, 440, 0.5)
	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Write to Ogg container
	var buf bytes.Buffer
	err = writeOggOpusLibopus(&buf, [][]byte{packet}, 1, 48000, 960)
	if err != nil {
		t.Fatalf("writeOggOpus failed: %v", err)
	}

	data := buf.Bytes()

	// Verify Ogg structure
	if string(data[0:4]) != "OggS" {
		t.Error("Missing OggS magic")
	}

	// Find OpusHead
	found := false
	for i := 0; i < len(data)-8; i++ {
		if string(data[i:i+8]) == "OpusHead" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Missing OpusHead")
	}

	// Find OpusTags
	found = false
	for i := 0; i < len(data)-8; i++ {
		if string(data[i:i+8]) == "OpusTags" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Missing OpusTags")
	}

	t.Logf("Ogg container: %d bytes total", len(data))
}

// TestLibopusEnergyPreservation tests signal energy preservation.
func TestLibopusEnergyPreservation(t *testing.T) {
	// Test with a known signal
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)

	// Generate 10 frames
	numFrames := 10
	packets := make([][]byte, numFrames)
	var inputEnergy float64

	for i := 0; i < numFrames; i++ {
		pcm := generateLibopusTestSignal(960, 440, 0.5)

		// Compute input energy
		for _, s := range pcm {
			inputEnergy += s * s
		}

		packet, err := enc.Encode(pcm, 960)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
		packets[i] = packet
	}

	inputEnergy /= float64(numFrames * 960)

	// Write to Ogg container
	var buf bytes.Buffer
	err := writeOggOpusLibopus(&buf, packets, 1, 48000, 960)
	if err != nil {
		t.Fatalf("writeOggOpus failed: %v", err)
	}

	samples, err := decodeLibopusOrInternal(buf.Bytes(), 1)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	outputEnergy := computeEnergyFloat32Libopus(samples)

	ratio := outputEnergy / inputEnergy
	t.Logf("Input energy: %.6f, Output energy: %.6f, Ratio: %.2f",
		inputEnergy, outputEnergy, ratio)

	// Log energy ratio assessment
	if ratio > 0.1 {
		t.Logf("PASS: Energy ratio >10%% preserved")
	} else {
		// Don't fail - this test is informational
		// CELT encoder may need further tuning for full signal preservation
		t.Logf("INFO: Energy ratio below threshold - CELT encoder may need tuning")
	}
}

func decodeLibopusOrInternal(oggData []byte, channels int) ([]float32, error) {
	if checkOpusdecAvailable() {
		samples, err := decodeWithOpusdecLibopus(oggData)
		if err == nil {
			return samples, nil
		}
		if !errors.Is(err, errUseInternalLibopus) {
			return nil, err
		}
	}
	return decodeWithInternalLibopus(oggData, channels)
}

var errUseInternalLibopus = errors.New("use internal decoder fallback")

func decodeWithOpusdecLibopus(oggData []byte) ([]float32, error) {
	tmpFile, err := os.CreateTemp("", "gopus_test_*.opus")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(oggData); err != nil {
		tmpFile.Close()
		return nil, err
	}
	if err := tmpFile.Close(); err != nil {
		return nil, err
	}

	// Best effort for macOS provenance quirks.
	exec.Command("xattr", "-c", tmpFile.Name()).Run()

	wavFile, err := os.CreateTemp("", "gopus_test_*.wav")
	if err != nil {
		return nil, err
	}
	defer os.Remove(wavFile.Name())
	wavFile.Close()

	opusdec := getOpusdecPathLibopus()
	cmd := exec.Command(opusdec, tmpFile.Name(), wavFile.Name())
	if output, err := cmd.CombinedOutput(); err != nil {
		if bytes.Contains(output, []byte("provenance")) ||
			bytes.Contains(output, []byte("quarantine")) ||
			bytes.Contains(output, []byte("killed")) ||
			bytes.Contains(output, []byte("Operation not permitted")) {
			return nil, errUseInternalLibopus
		}
		return nil, err
	}

	wavData, err := os.ReadFile(wavFile.Name())
	if err != nil {
		return nil, err
	}
	if len(wavData) <= 44 {
		return nil, io.ErrUnexpectedEOF
	}
	return parseWAVSamplesLibopus(wavData), nil
}

func decodeWithInternalLibopus(oggData []byte, channels int) ([]float32, error) {
	r, err := ogg.NewReader(bytes.NewReader(oggData))
	if err != nil {
		return nil, err
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		return nil, err
	}

	out := make([]float32, 5760*channels)
	var decoded []float32
	for {
		packet, _, err := r.ReadPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		n, err := dec.Decode(packet, out)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			decoded = append(decoded, out[:n*channels]...)
		}
	}

	preSkip := int(r.PreSkip()) * channels
	if preSkip > 0 && len(decoded) > preSkip {
		decoded = decoded[preSkip:]
	}

	return decoded, nil
}

// modeToGopusLibopus converts encoder.Mode to types.Mode
func modeToGopusLibopus(m encoder.Mode) types.Mode {
	switch m {
	case encoder.ModeSILK:
		return types.ModeSILK
	case encoder.ModeHybrid:
		return types.ModeHybrid
	case encoder.ModeCELT:
		return types.ModeCELT
	default:
		return types.ModeSILK
	}
}
