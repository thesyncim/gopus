// Package testvectors provides bit-exact encoding comparison tests.
// This file compares gopus encoder output byte-by-byte with libopus.
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

// TestBitExactComparison compares gopus encoder output with libopus byte-by-byte.
func TestBitExactComparison(t *testing.T) {
	if !checkOpusencAvailable() {
		t.Skip("opusenc not found in PATH")
	}

	// Test with simple sine wave - easiest to debug
	tests := []struct {
		name      string
		freq      float64
		frameSize int
		channels  int
		bitrate   int
	}{
		{"440Hz-20ms-mono-64k", 440, 960, 1, 64000},
		{"1kHz-20ms-mono-64k", 1000, 960, 1, 64000},
		{"440Hz-10ms-mono-64k", 440, 480, 1, 64000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			compareBitExact(t, tc.freq, tc.frameSize, tc.channels, tc.bitrate)
		})
	}
}

// compareBitExact encodes the same audio with both encoders and compares packets.
func compareBitExact(t *testing.T, freq float64, frameSize, channels, bitrate int) {
	// Generate test signal - single frame for simplicity
	numFrames := 5 // Encode a few frames
	totalSamples := numFrames * frameSize * channels

	// Generate pure sine wave at specified frequency
	pcmF32 := generateSineWave(totalSamples, channels, freq, 48000, 0.5)

	// Convert to int16 for libopus (raw PCM)
	pcmS16 := float32ToInt16(pcmF32)

	// Encode with gopus
	gopusPackets := encodeWithGopus(t, pcmF32, frameSize, channels, bitrate)

	// Encode with libopus
	libopusPackets := encodeWithLibopus(t, pcmS16, frameSize, channels, bitrate)

	// Compare packets
	t.Logf("gopus packets: %d, libopus packets: %d", len(gopusPackets), len(libopusPackets))

	minPackets := len(gopusPackets)
	if len(libopusPackets) < minPackets {
		minPackets = len(libopusPackets)
	}

	for i := 0; i < minPackets; i++ {
		gp := gopusPackets[i]
		lp := libopusPackets[i]

		t.Logf("Frame %d: gopus=%d bytes, libopus=%d bytes", i, len(gp), len(lp))

		// Compare TOC byte
		if len(gp) > 0 && len(lp) > 0 {
			t.Logf("  TOC: gopus=0x%02x, libopus=0x%02x", gp[0], lp[0])
			if gp[0] != lp[0] {
				t.Logf("  TOC MISMATCH! gopus config=%d, libopus config=%d",
					gp[0]>>3, lp[0]>>3)
			}
		}

		// Find first divergence point
		divergeAt := -1
		minLen := len(gp)
		if len(lp) < minLen {
			minLen = len(lp)
		}

		for j := 0; j < minLen; j++ {
			if gp[j] != lp[j] {
				divergeAt = j
				break
			}
		}

		if divergeAt >= 0 {
			t.Logf("  DIVERGE at byte %d: gopus=0x%02x, libopus=0x%02x",
				divergeAt, gp[divergeAt], lp[divergeAt])

			// Show context around divergence
			showContext(t, gp, lp, divergeAt)
		} else if len(gp) != len(lp) {
			t.Logf("  LENGTH MISMATCH: gopus=%d, libopus=%d", len(gp), len(lp))
		} else {
			t.Logf("  MATCH! All %d bytes identical", len(gp))
		}

		// Detailed hex dump for first frame
		if i == 0 {
			t.Logf("  gopus hex:   %x", gp)
			t.Logf("  libopus hex: %x", lp)
		}
	}
}

// showContext shows bytes around a divergence point.
func showContext(t *testing.T, gp, lp []byte, divergeAt int) {
	start := divergeAt - 3
	if start < 0 {
		start = 0
	}
	end := divergeAt + 5
	if end > len(gp) {
		end = len(gp)
	}
	if end > len(lp) {
		end = len(lp)
	}

	t.Logf("  Context (bytes %d-%d):", start, end-1)
	t.Logf("    gopus:   %x", gp[start:end])
	t.Logf("    libopus: %x", lp[start:end])

	// Show bit-level comparison at divergence point
	if divergeAt < len(gp) && divergeAt < len(lp) {
		gb := gp[divergeAt]
		lb := lp[divergeAt]
		t.Logf("  Bit comparison at byte %d:", divergeAt)
		t.Logf("    gopus:   %08b", gb)
		t.Logf("    libopus: %08b", lb)
		xor := gb ^ lb
		t.Logf("    XOR:     %08b (differs in %d bits)", xor, countBits(xor))
	}
}

// countBits counts the number of 1 bits.
func countBits(b byte) int {
	count := 0
	for b != 0 {
		count += int(b & 1)
		b >>= 1
	}
	return count
}

// generateSineWave generates a sine wave test signal.
func generateSineWave(samples, channels int, freq, sampleRate, amplitude float64) []float32 {
	signal := make([]float32, samples)
	for i := 0; i < samples; i++ {
		ch := i % channels
		sampleIdx := i / channels
		t := float64(sampleIdx) / sampleRate
		val := amplitude * math.Sin(2*math.Pi*freq*t)
		// Slight offset for stereo channels
		if channels == 2 && ch == 1 {
			val = amplitude * math.Sin(2*math.Pi*freq*1.01*t)
		}
		signal[i] = float32(val)
	}
	return signal
}

// float32ToInt16 converts float32 samples to int16.
func float32ToInt16(f []float32) []int16 {
	s := make([]int16, len(f))
	for i, v := range f {
		// Clamp to valid range
		if v > 1.0 {
			v = 1.0
		}
		if v < -1.0 {
			v = -1.0
		}
		scaled := float64(v) * 32768.0
		if scaled > 32767.0 {
			s[i] = 32767
		} else if scaled < -32768.0 {
			s[i] = -32768
		} else {
			s[i] = int16(math.RoundToEven(scaled))
		}
	}
	return s
}

// encodeWithGopus encodes audio using the gopus encoder.
func encodeWithGopus(t *testing.T, pcmF32 []float32, frameSize, channels, bitrate int) [][]byte {
	enc := encoder.NewEncoder(48000, channels)
	enc.SetMode(encoder.ModeCELT) // CELT only for now
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(bitrate)
	enc.SetBitrateMode(encoder.ModeCBR) // Match libopus --hard-cbr

	var packets [][]byte
	samplesPerFrame := frameSize * channels

	for i := 0; i+samplesPerFrame <= len(pcmF32); i += samplesPerFrame {
		frame := pcmF32[i : i+samplesPerFrame]
		pcmF64 := make([]float64, len(frame))
		for j, v := range frame {
			pcmF64[j] = float64(v)
		}

		packet, err := enc.Encode(pcmF64, frameSize)
		if err != nil {
			t.Fatalf("gopus encode failed: %v", err)
		}
		// Copy packet since Encode returns a slice backed by scratch memory.
		packetCopy := make([]byte, len(packet))
		copy(packetCopy, packet)
		packets = append(packets, packetCopy)
	}

	return packets
}

// encodeWithLibopus encodes audio using libopus via opusenc CLI.
func encodeWithLibopus(t *testing.T, pcmS16 []int16, frameSize, channels, bitrate int) [][]byte {
	// Write raw PCM to temp file
	rawFile, err := os.CreateTemp("", "bitexact_*.raw")
	if err != nil {
		t.Fatalf("create temp raw file: %v", err)
	}
	defer os.Remove(rawFile.Name())

	// Write int16 samples as little-endian
	for _, s := range pcmS16 {
		binary.Write(rawFile, binary.LittleEndian, s)
	}
	rawFile.Close()

	// Create output file
	opusFile, err := os.CreateTemp("", "bitexact_*.opus")
	if err != nil {
		t.Fatalf("create temp opus file: %v", err)
	}
	defer os.Remove(opusFile.Name())
	opusFile.Close()

	// Encode with opusenc
	// Use --hard-cbr for constant bitrate (more predictable)
	// Use --framesize to match our frame size
	frameSizeMs := frameSize * 1000 / 48000
	args := []string{
		"--raw",
		"--raw-rate", "48000",
		"--raw-chan", fmt.Sprintf("%d", channels),
		"--raw-bits", "16",
		"--bitrate", fmt.Sprintf("%d", bitrate/1000),
		"--hard-cbr",
		"--comp", "0", // Lowest complexity for predictability
		"--framesize", fmt.Sprintf("%d", frameSizeMs),
		rawFile.Name(),
		opusFile.Name(),
	}

	cmd := exec.Command("opusenc", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opusenc failed: %v\nOutput: %s", err, output)
	}

	// Read Ogg Opus file and extract packets
	data, err := os.ReadFile(opusFile.Name())
	if err != nil {
		t.Fatalf("read opus file: %v", err)
	}

	packets := extractOggOpusPackets(data)
	return packets
}

// extractOggOpusPackets extracts Opus packets from an Ogg container.
// Skips the OpusHead and OpusTags headers.
func extractOggOpusPackets(data []byte) [][]byte {
	var packets [][]byte
	offset := 0

	pageNum := 0
	for offset+27 < len(data) {
		// Check Ogg page header
		if string(data[offset:offset+4]) != "OggS" {
			break
		}

		// Parse page header
		// headerType := data[offset+5]
		// granulePos := binary.LittleEndian.Uint64(data[offset+6:offset+14])
		numSegments := int(data[offset+26])

		if offset+27+numSegments > len(data) {
			break
		}

		// Read segment table
		segmentTable := data[offset+27 : offset+27+numSegments]

		// Calculate total page data size
		pageDataSize := 0
		for _, s := range segmentTable {
			pageDataSize += int(s)
		}

		pageDataStart := offset + 27 + numSegments
		if pageDataStart+pageDataSize > len(data) {
			break
		}

		// Extract packets from this page
		pageData := data[pageDataStart : pageDataStart+pageDataSize]

		// Segment packets (multiple segments can form one packet)
		packetData := []byte{}
		segOffset := 0
		for _, segSize := range segmentTable {
			if segOffset+int(segSize) > len(pageData) {
				break
			}
			packetData = append(packetData, pageData[segOffset:segOffset+int(segSize)]...)
			segOffset += int(segSize)

			// Packet complete if segment < 255 bytes
			if segSize < 255 {
				// Skip OpusHead and OpusTags headers (first 2 pages)
				if pageNum >= 2 && len(packetData) > 0 {
					pkt := make([]byte, len(packetData))
					copy(pkt, packetData)
					packets = append(packets, pkt)
				}
				packetData = []byte{}
			}
		}

		// If we have leftover data, it's a partial packet continuing to next page
		// (handled by segment table concatenation)

		offset = pageDataStart + pageDataSize
		pageNum++
	}

	return packets
}

// checkOpusencAvailable checks if opusenc is available.
func checkOpusencAvailable() bool {
	_, err := exec.LookPath("opusenc")
	return err == nil
}

// TestAnalyzeLibopusPacket decodes a libopus packet to understand its structure.
func TestAnalyzeLibopusPacket(t *testing.T) {
	if !checkOpusencAvailable() {
		t.Skip("opusenc not found")
	}

	// Generate a simple test signal
	pcmF32 := generateSineWave(960, 1, 440, 48000, 0.5)
	pcmS16 := float32ToInt16(pcmF32)

	// Encode with libopus
	packets := encodeWithLibopus(t, pcmS16, 960, 1, 64000)
	if len(packets) == 0 {
		t.Fatal("No packets from libopus")
	}

	pkt := packets[0]
	t.Logf("Libopus packet: %d bytes", len(pkt))
	t.Logf("Hex: %x", pkt)

	// Analyze TOC byte
	if len(pkt) > 0 {
		toc := pkt[0]
		config := toc >> 3
		stereo := (toc >> 2) & 1
		frameCode := toc & 3

		t.Logf("TOC byte: 0x%02x", toc)
		t.Logf("  Config: %d", config)
		t.Logf("  Stereo: %d", stereo)
		t.Logf("  FrameCode: %d", frameCode)

		// Decode config
		mode := "unknown"
		if config <= 11 {
			mode = "SILK"
		} else if config <= 15 {
			mode = "Hybrid"
		} else {
			mode = "CELT"
		}
		t.Logf("  Mode: %s", mode)
	}
}

// TestRangeCoderComparison compares range coder output for simple sequences.
func TestRangeCoderComparison(t *testing.T) {
	// This test encodes a simple bit sequence and checks the output
	// against expected libopus behavior.

	// We need to understand the exact byte ordering libopus uses.
	// The key insight is that libopus uses a specific carry propagation
	// and byte complementing scheme.

	// For now, let's just document what we observe from libopus output.
	t.Log("Range coder comparison - observing libopus patterns")

	if !checkOpusencAvailable() {
		t.Skip("opusenc not found")
	}

	// Encode silence (simplest case)
	silentPCM := make([]float32, 960)
	silentS16 := float32ToInt16(silentPCM)
	packets := encodeWithLibopus(t, silentS16, 960, 1, 64000)

	if len(packets) > 0 {
		t.Logf("Silent frame packet: %x", packets[0])
		// Analyze what libopus encodes for silence
	}
}

// TestFrameByFrameAnalysis does detailed analysis of each encoding step.
func TestFrameByFrameAnalysis(t *testing.T) {
	if !checkOpusencAvailable() {
		t.Skip("opusenc not found")
	}

	// Very simple: single sine wave frame
	pcmF32 := generateSineWave(960, 1, 440, 48000, 0.3)

	// Get gopus encoding with detailed logging
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)

	pcmF64 := make([]float64, len(pcmF32))
	for i, v := range pcmF32 {
		pcmF64[i] = float64(v)
	}

	packet, err := enc.Encode(pcmF64, 960)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	t.Logf("gopus packet: %d bytes", len(packet))
	t.Logf("gopus hex: %x", packet)

	// Compare with libopus
	pcmS16 := float32ToInt16(pcmF32)
	libPackets := encodeWithLibopus(t, pcmS16, 960, 1, 64000)
	if len(libPackets) > 0 {
		t.Logf("libopus packet: %d bytes", len(libPackets[0]))
		t.Logf("libopus hex: %x", libPackets[0])
	}
}

// TestMinimalEncoding tests the simplest possible encoding scenario.
func TestMinimalEncoding(t *testing.T) {
	if !checkOpusencAvailable() {
		t.Skip("opusenc not found")
	}

	// DC signal (constant value) - simplest possible audio
	dcPCM := make([]float32, 960*5) // 5 frames
	for i := range dcPCM {
		dcPCM[i] = 0.1 // Small constant
	}

	pcmS16 := float32ToInt16(dcPCM)
	libPackets := encodeWithLibopus(t, pcmS16, 960, 1, 64000)

	t.Logf("DC signal encoding with libopus:")
	for i, pkt := range libPackets {
		t.Logf("  Frame %d: %d bytes - %x", i, len(pkt), pkt)
	}

	// Same with gopus
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)

	t.Logf("DC signal encoding with gopus:")
	for i := 0; i < 5; i++ {
		start := i * 960
		pcmF64 := make([]float64, 960)
		for j := 0; j < 960; j++ {
			pcmF64[j] = float64(dcPCM[start+j])
		}
		packet, err := enc.Encode(pcmF64, 960)
		if err != nil {
			t.Fatalf("Frame %d encode failed: %v", i, err)
		}
		t.Logf("  Frame %d: %d bytes - %x", i, len(packet), packet)
	}
}

// writeOggOpusForLibopus writes packets to Ogg for decoding verification.
func writeOggOpusForLibopus(w io.Writer, packets [][]byte, channels int) error {
	serialNo := uint32(12345)
	var granulePos uint64

	// Page 1: OpusHead
	opusHead := make([]byte, 19)
	copy(opusHead[0:8], "OpusHead")
	opusHead[8] = 1
	opusHead[9] = byte(channels)
	binary.LittleEndian.PutUint16(opusHead[10:12], 312) // Pre-skip
	binary.LittleEndian.PutUint32(opusHead[12:16], 48000)
	binary.LittleEndian.PutUint16(opusHead[16:18], 0)
	opusHead[18] = 0

	if err := writeOggPage(w, serialNo, 0, 2, 0, [][]byte{opusHead}); err != nil {
		return err
	}

	// Page 2: OpusTags
	tags := []byte("OpusTags\x05\x00\x00\x00gopus\x00\x00\x00\x00")
	if err := writeOggPage(w, serialNo, 1, 0, 0, [][]byte{tags}); err != nil {
		return err
	}

	// Data pages
	pageNo := uint32(2)
	granulePos = 312 // Start after pre-skip

	for i, packet := range packets {
		granulePos += 960 // 20ms at 48kHz
		headerType := byte(0)
		if i == len(packets)-1 {
			headerType = 4
		}
		if err := writeOggPage(w, serialNo, pageNo, headerType, granulePos, [][]byte{packet}); err != nil {
			return err
		}
		pageNo++
	}

	return nil
}

// writeOggPage, oggCRC, oggCRCUpdate are defined in ogg_helpers_test.go

// TestVerifyGopusDecodable verifies gopus output can be decoded by libopus.
func TestVerifyGopusDecodable(t *testing.T) {
	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not found")
	}

	// Generate test signal
	pcmF32 := generateSineWave(960*5, 1, 440, 48000, 0.5)

	// Encode with gopus
	packets := encodeWithGopus(t, pcmF32, 960, 1, 64000)

	// Write to Ogg container
	var buf bytes.Buffer
	if err := writeOggOpusForLibopus(&buf, packets, 1); err != nil {
		t.Fatalf("Write Ogg failed: %v", err)
	}

	// Write to temp file
	tmpFile, err := os.CreateTemp("", "gopus_verify_*.opus")
	if err != nil {
		t.Fatalf("Create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write(buf.Bytes())
	tmpFile.Close()

	// Decode with opusdec
	wavFile, err := os.CreateTemp("", "gopus_verify_*.wav")
	if err != nil {
		t.Fatalf("Create temp wav: %v", err)
	}
	defer os.Remove(wavFile.Name())
	wavFile.Close()

	cmd := exec.Command("opusdec", tmpFile.Name(), wavFile.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("opusdec output: %s", output)
		t.Fatalf("opusdec failed: %v", err)
	}

	t.Log("gopus output successfully decoded by libopus")

	// Read decoded WAV and check quality
	wavData, _ := os.ReadFile(wavFile.Name())
	decoded := parseWAVSamplesEncoder(wavData)

	// Strip pre-skip
	if len(decoded) > 312 {
		decoded = decoded[312:]
	}

	// Compare with original
	compareLen := len(pcmF32)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}

	q := ComputeQualityFloat32(decoded[:compareLen], pcmF32[:compareLen], 48000)
	t.Logf("Quality: Q=%.2f, SNR=%.2f dB", q, SNRFromQuality(q))
}
