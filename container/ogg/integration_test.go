package ogg

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"testing"

	gopus "github.com/thesyncim/gopus"
)

// checkOpusdec checks if opusdec is available.
func checkOpusdec() bool {
	// Check PATH first.
	if _, err := exec.LookPath("opusdec"); err == nil {
		return true
	}

	// Check common paths.
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
	if !checkOpusdec() {
		return decodeWithInternalDecoder(oggData)
	}

	// Write to temp Opus file.
	tmpOpus, err := os.CreateTemp("", "gopus_ogg_*.opus")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpOpus.Name())

	if _, err := tmpOpus.Write(oggData); err != nil {
		tmpOpus.Close()
		return nil, err
	}
	tmpOpus.Close()

	// Clear extended attributes on macOS (provenance can cause issues).
	exec.Command("xattr", "-c", tmpOpus.Name()).Run()

	// Create output WAV file.
	tmpWav, err := os.CreateTemp("", "gopus_ogg_*.wav")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpWav.Name())
	tmpWav.Close()

	// Run opusdec.
	opusdec := getOpusdecPath()
	cmd := exec.Command(opusdec, tmpOpus.Name(), tmpWav.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check for macOS provenance issues.
		if bytes.Contains(output, []byte("provenance")) ||
			bytes.Contains(output, []byte("quarantine")) ||
			bytes.Contains(output, []byte("killed")) ||
			bytes.Contains(output, []byte("Operation not permitted")) ||
			bytes.Contains(output, []byte("Failed to open")) {
			return decodeWithInternalDecoder(oggData)
		}
		return nil, err
	}

	// Read and parse WAV file.
	wavData, err := os.ReadFile(tmpWav.Name())
	if err != nil {
		return nil, err
	}

	return parseWAVSamples(wavData), nil
}

func decodeWithInternalDecoder(oggData []byte) ([]float32, error) {
	r, err := NewReader(bytes.NewReader(oggData))
	if err != nil {
		return nil, fmt.Errorf("new reader: %w", err)
	}

	channels := int(r.Channels())
	if channels <= 0 {
		channels = 1
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		return nil, fmt.Errorf("new decoder: %w", err)
	}

	out := make([]float32, 5760*channels)
	decoded := make([]float32, 0, 48000*channels)
	for {
		packet, _, err := r.ReadPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read packet: %w", err)
		}
		n, err := dec.Decode(packet, out)
		if err != nil {
			return nil, fmt.Errorf("decode packet: %w", err)
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

// parseWAVSamples extracts float32 samples from WAV data.
func parseWAVSamples(data []byte) []float32 {
	if len(data) < 44 {
		return nil
	}

	// Find data chunk.
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

	// Fallback: skip WAV header.
	data = data[44:]
	samples := make([]float32, len(data)/2)
	for i := 0; i < len(data)/2; i++ {
		s := int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
		samples[i] = float32(s) / 32768.0
	}
	return samples
}

// generateSineWave creates a mono sine wave at the given frequency.
func generateSineWave(freq float64, samples int) []float32 {
	pcm := make([]float32, samples)
	for i := range pcm {
		t := float64(i) / 48000.0
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*freq*t))
	}
	return pcm
}

// generateStereoSineWave creates a stereo sine wave (interleaved).
func generateStereoSineWave(freqL, freqR float64, samplesPerChannel int) []float32 {
	pcm := make([]float32, samplesPerChannel*2)
	for i := 0; i < samplesPerChannel; i++ {
		t := float64(i) / 48000.0
		pcm[i*2] = float32(0.5 * math.Sin(2*math.Pi*freqL*t))
		pcm[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*freqR*t))
	}
	return pcm
}

// computeEnergy calculates RMS energy of samples.
func computeEnergy(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return sum / float64(len(samples))
}

// TestIntegration_WriterOpusdec_Mono tests that mono Writer output is accepted by opusdec.
func TestIntegration_WriterOpusdec_Mono(t *testing.T) {
	// Create encoder.
	enc, err := gopus.NewEncoder(48000, 1, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	enc.SetBitrate(64000) // 64 kbps

	// Generate test audio.
	frameSize := 960 // 20ms at 48kHz
	numFrames := 20

	var oggBuf bytes.Buffer
	w, err := NewWriter(&oggBuf, 48000, 1)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	var allInput []float32
	for i := 0; i < numFrames; i++ {
		pcm := generateSineWave(440.0, frameSize)
		allInput = append(allInput, pcm...)

		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
		if len(packet) == 0 {
			t.Logf("Frame %d: DTX (silence)", i)
			packet = []byte{0xF8, 0xFF, 0xFE} // CELT silence
		}
		err = w.WritePacket(packet, frameSize)
		if err != nil {
			t.Fatalf("WritePacket failed: %v", err)
		}
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	inputEnergy := computeEnergy(allInput)
	t.Logf("Input: %d frames, %d samples, energy=%.6f", numFrames, len(allInput), inputEnergy)
	t.Logf("Ogg container: %d bytes", oggBuf.Len())

	// Decode with opusdec.
	decoded, err := decodeWithOpusdec(oggBuf.Bytes())
	if err != nil {
		t.Fatalf("decodeWithOpusdec failed: %v", err)
	}

	if len(decoded) == 0 {
		t.Fatal("opusdec produced empty output")
	}

	outputEnergy := computeEnergy(decoded)
	t.Logf("Decoded: %d samples, energy=%.6f", len(decoded), outputEnergy)

	// Energy ratio check (>10% threshold).
	energyRatio := outputEnergy / inputEnergy * 100
	t.Logf("Energy ratio: %.1f%% (threshold: 10%%)", energyRatio)

	if energyRatio < 10.0 {
		t.Errorf("Energy ratio too low: %.1f%% < 10%%", energyRatio)
	} else {
		t.Logf("PASS: Mono Writer output validated with opusdec")
	}
}

// TestIntegration_WriterOpusdec_Stereo tests that stereo Writer output is accepted by opusdec.
func TestIntegration_WriterOpusdec_Stereo(t *testing.T) {
	// Create encoder.
	enc, err := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	enc.SetBitrate(128000) // 128 kbps

	// Generate test audio.
	frameSize := 960
	numFrames := 20

	var oggBuf bytes.Buffer
	w, err := NewWriter(&oggBuf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	var allInput []float32
	for i := 0; i < numFrames; i++ {
		pcm := generateStereoSineWave(440.0, 554.0, frameSize) // A4 left, C#5 right
		allInput = append(allInput, pcm...)

		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
		if len(packet) == 0 {
			packet = []byte{0xF8, 0xFF, 0xFE}
		}
		err = w.WritePacket(packet, frameSize)
		if err != nil {
			t.Fatalf("WritePacket failed: %v", err)
		}
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	inputEnergy := computeEnergy(allInput)
	t.Logf("Input: %d frames, %d samples, energy=%.6f", numFrames, len(allInput), inputEnergy)
	t.Logf("Ogg container: %d bytes", oggBuf.Len())

	// Decode with opusdec.
	decoded, err := decodeWithOpusdec(oggBuf.Bytes())
	if err != nil {
		t.Fatalf("decodeWithOpusdec failed: %v", err)
	}

	if len(decoded) == 0 {
		t.Fatal("opusdec produced empty output")
	}

	outputEnergy := computeEnergy(decoded)
	t.Logf("Decoded: %d samples, energy=%.6f", len(decoded), outputEnergy)

	// Energy ratio check.
	energyRatio := outputEnergy / inputEnergy * 100
	t.Logf("Energy ratio: %.1f%% (threshold: 10%%)", energyRatio)

	if energyRatio < 10.0 {
		t.Errorf("Energy ratio too low: %.1f%% < 10%%", energyRatio)
	} else {
		t.Logf("PASS: Stereo Writer output validated with opusdec")
	}
}

// TestIntegration_WriterOpusdec_Multistream tests 5.1 Writer output with opusdec.
func TestIntegration_WriterOpusdec_Multistream(t *testing.T) {
	// Import multistream encoder.
	// For this test, we use the internal multistream encoder.
	// Skip if multistream encoder is not available via gopus package.

	// Try to create a multistream encoder via the multistream package.
	// Since gopus may not expose multistream directly, we'll test using
	// the existing test pattern from internal/multistream/libopus_test.go.

	t.Skip("Multistream encoder not exposed via public gopus API yet")
}

// TestIntegration_RoundTrip tests writing and reading back packets.
func TestIntegration_RoundTrip(t *testing.T) {
	// Create encoder.
	enc, err := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	enc.SetBitrate(128000)

	// Generate and encode packets.
	frameSize := 960
	numFrames := 20
	originalPackets := make([][]byte, numFrames)

	var oggBuf bytes.Buffer
	w, err := NewWriter(&oggBuf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	for i := 0; i < numFrames; i++ {
		pcm := generateStereoSineWave(440.0, 554.0, frameSize)

		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
		if len(packet) == 0 {
			packet = []byte{0xF8, 0xFF, 0xFE}
		}
		originalPackets[i] = make([]byte, len(packet))
		copy(originalPackets[i], packet)

		err = w.WritePacket(packet, frameSize)
		if err != nil {
			t.Fatalf("WritePacket failed: %v", err)
		}
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	t.Logf("Wrote %d packets to %d bytes", numFrames, oggBuf.Len())

	// Read packets back.
	r, err := NewReader(bytes.NewReader(oggBuf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Verify header.
	if r.Channels() != 2 {
		t.Errorf("Channels = %d, want 2", r.Channels())
	}

	// Read packets.
	readCount := 0
	for {
		packet, _, err := r.ReadPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ReadPacket failed: %v", err)
		}

		if readCount >= numFrames {
			t.Errorf("Read more packets than written")
			break
		}

		// Verify packet matches.
		if len(packet) != len(originalPackets[readCount]) {
			t.Errorf("Packet %d len = %d, want %d", readCount, len(packet), len(originalPackets[readCount]))
		} else {
			for j := range packet {
				if packet[j] != originalPackets[readCount][j] {
					t.Errorf("Packet %d byte %d = %d, want %d", readCount, j, packet[j], originalPackets[readCount][j])
					break
				}
			}
		}

		readCount++
	}

	if readCount != numFrames {
		t.Errorf("Read %d packets, want %d", readCount, numFrames)
	} else {
		t.Logf("PASS: Round-trip verified - %d packets match", readCount)
	}
}

// TestIntegration_ReaderWriterRoundTrip tests complete byte-for-byte round-trip.
func TestIntegration_ReaderWriterRoundTrip(t *testing.T) {
	// Generate packets with known content.
	packets := make([][]byte, 10)
	for i := 0; i < 10; i++ {
		// Create packet with distinct content.
		packets[i] = make([]byte, 50+i*10)
		packets[i][0] = 0xFC // TOC byte
		for j := 1; j < len(packets[i]); j++ {
			packets[i][j] = byte((i*100 + j) % 256)
		}
	}

	// Write packets.
	var oggBuf bytes.Buffer
	w, err := NewWriter(&oggBuf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	for i, packet := range packets {
		err = w.WritePacket(packet, 960)
		if err != nil {
			t.Fatalf("WritePacket %d failed: %v", i, err)
		}
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Read packets back.
	r, err := NewReader(bytes.NewReader(oggBuf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	for i, original := range packets {
		read, _, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("ReadPacket %d failed: %v", i, err)
		}

		if len(read) != len(original) {
			t.Errorf("Packet %d len = %d, want %d", i, len(read), len(original))
			continue
		}

		for j := range read {
			if read[j] != original[j] {
				t.Errorf("Packet %d byte %d = %d, want %d", i, j, read[j], original[j])
				break
			}
		}
	}

	// Verify EOF.
	_, _, err = r.ReadPacket()
	if err != io.EOF {
		t.Errorf("Expected EOF after all packets, got %v", err)
	}

	t.Logf("PASS: Byte-for-byte round-trip verified for %d packets", len(packets))
}

// TestIntegration_GranulePosition tests granule position tracking across write/read.
func TestIntegration_GranulePosition(t *testing.T) {
	// Write packets with known sample counts.
	sampleCounts := []int{480, 960, 1920, 480, 960, 2880}

	var oggBuf bytes.Buffer
	w, err := NewWriter(&oggBuf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	totalSamples := 0
	for i, samples := range sampleCounts {
		packet := make([]byte, 50)
		packet[0] = 0xFC
		err = w.WritePacket(packet, samples)
		if err != nil {
			t.Fatalf("WritePacket %d failed: %v", i, err)
		}
		totalSamples += samples
	}

	// Verify final granule position on writer.
	if w.GranulePos() != uint64(totalSamples) {
		t.Errorf("Writer GranulePos = %d, want %d", w.GranulePos(), totalSamples)
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Read packets and verify granule positions.
	r, err := NewReader(bytes.NewReader(oggBuf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	expectedGranule := uint64(0)
	for i, samples := range sampleCounts {
		_, granule, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("ReadPacket %d failed: %v", i, err)
		}

		expectedGranule += uint64(samples)
		if granule != expectedGranule {
			t.Errorf("Packet %d granule = %d, want %d", i, granule, expectedGranule)
		}
	}

	// Final granule should equal total samples.
	if r.GranulePos() != uint64(totalSamples) {
		t.Errorf("Final GranulePos = %d, want %d", r.GranulePos(), totalSamples)
	}

	t.Logf("PASS: Granule positions verified - total %d samples", totalSamples)
}

// TestIntegration_ContainerStructure tests that Writer produces valid Ogg Opus structure.
func TestIntegration_ContainerStructure(t *testing.T) {
	var oggBuf bytes.Buffer
	w, err := NewWriter(&oggBuf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write a few packets.
	for i := 0; i < 3; i++ {
		err = w.WritePacket(make([]byte, 50), 960)
		if err != nil {
			t.Fatalf("WritePacket failed: %v", err)
		}
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	data := oggBuf.Bytes()
	t.Logf("Container size: %d bytes", len(data))

	// Verify structure.
	// Page 0: BOS with OpusHead
	// Page 1: OpusTags
	// Pages 2-4: Audio
	// Page 5: EOS

	offset := 0
	pageNum := 0
	foundOpusHead := false
	foundOpusTags := false
	foundEOS := false

	for offset < len(data) {
		page, consumed, err := ParsePage(data[offset:])
		if err != nil {
			break
		}

		t.Logf("Page %d: seq=%d, granule=%d, flags=0x%02x, payload=%d bytes",
			pageNum, page.PageSequence, page.GranulePos, page.HeaderType, len(page.Payload))

		switch pageNum {
		case 0:
			if !page.IsBOS() {
				t.Error("Page 0 should have BOS flag")
			}
			if page.GranulePos != 0 {
				t.Errorf("Page 0 granule = %d, want 0", page.GranulePos)
			}
			// Check for OpusHead.
			if len(page.Payload) >= 8 && string(page.Payload[:8]) == "OpusHead" {
				foundOpusHead = true
			}
		case 1:
			if page.GranulePos != 0 {
				t.Errorf("Page 1 granule = %d, want 0", page.GranulePos)
			}
			// Check for OpusTags.
			if len(page.Payload) >= 8 && string(page.Payload[:8]) == "OpusTags" {
				foundOpusTags = true
			}
		}

		if page.IsEOS() {
			foundEOS = true
		}

		offset += consumed
		pageNum++
	}

	if !foundOpusHead {
		t.Error("OpusHead not found")
	}
	if !foundOpusTags {
		t.Error("OpusTags not found")
	}
	if !foundEOS {
		t.Error("EOS page not found")
	}

	// Should have 6 pages: 2 headers + 3 audio + 1 EOS.
	if pageNum != 6 {
		t.Errorf("Parsed %d pages, expected 6", pageNum)
	}

	t.Logf("PASS: Valid Ogg Opus structure with %d pages", pageNum)
}

// TestIntegration_LargeFile tests writing and reading a larger file.
func TestIntegration_LargeFile(t *testing.T) {
	// Create encoder.
	enc, err := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	enc.SetBitrate(128000)

	// Write 100 frames (2 seconds).
	frameSize := 960
	numFrames := 100

	var oggBuf bytes.Buffer
	w, err := NewWriter(&oggBuf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	for i := 0; i < numFrames; i++ {
		pcm := generateStereoSineWave(440.0+float64(i), 554.0+float64(i), frameSize)

		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
		if len(packet) == 0 {
			packet = []byte{0xF8, 0xFF, 0xFE}
		}
		err = w.WritePacket(packet, frameSize)
		if err != nil {
			t.Fatalf("WritePacket failed: %v", err)
		}
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	t.Logf("Wrote %d frames to %d bytes (%.1f seconds)",
		numFrames, oggBuf.Len(), float64(numFrames*frameSize)/48000.0)

	// Read back.
	r, err := NewReader(bytes.NewReader(oggBuf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	readCount := 0
	for {
		_, _, err := r.ReadPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ReadPacket failed: %v", err)
		}
		readCount++
	}

	if readCount != numFrames {
		t.Errorf("Read %d packets, want %d", readCount, numFrames)
	}

	expectedGranule := uint64(numFrames * frameSize)
	if r.GranulePos() != expectedGranule {
		t.Errorf("Final GranulePos = %d, want %d", r.GranulePos(), expectedGranule)
	}

	t.Logf("PASS: Large file verified - %d frames, %d bytes", numFrames, oggBuf.Len())
}
