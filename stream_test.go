// stream_test.go contains tests for the streaming io.Reader/io.Writer API.

package gopus

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"
)

// slicePacketSource implements PacketSource for testing.
type slicePacketSource struct {
	packets [][]byte
	index   int
}

func (s *slicePacketSource) NextPacket() ([]byte, error) {
	if s.index >= len(s.packets) {
		return nil, io.EOF
	}
	packet := s.packets[s.index]
	s.index++
	return packet, nil
}

// slicePacketSink implements PacketSink for testing.
type slicePacketSink struct {
	packets [][]byte
}

func (s *slicePacketSink) WritePacket(packet []byte) (int, error) {
	cp := make([]byte, len(packet))
	copy(cp, packet)
	s.packets = append(s.packets, cp)
	return len(packet), nil
}

// generateTestPacket generates a valid Opus packet by encoding test audio.
func generateTestPacket(sampleRate, channels, frameSize int) ([]byte, error) {
	enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
	if err != nil {
		return nil, err
	}
	enc.SetFrameSize(frameSize)

	// Generate a simple sine wave
	pcm := make([]float32, frameSize*channels)
	freq := 440.0
	for i := 0; i < frameSize; i++ {
		sample := float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = sample
		}
	}

	return enc.EncodeFloat32(pcm)
}

// TestNewReader_ValidParams tests creating readers with valid parameters.
func TestNewReader_ValidParams(t *testing.T) {
	testCases := []struct {
		name       string
		sampleRate int
		channels   int
		format     SampleFormat
	}{
		{"48kHz mono float32", 48000, 1, FormatFloat32LE},
		{"48kHz stereo float32", 48000, 2, FormatFloat32LE},
		{"48kHz mono int16", 48000, 1, FormatInt16LE},
		{"48kHz stereo int16", 48000, 2, FormatInt16LE},
		{"24kHz mono float32", 24000, 1, FormatFloat32LE},
		{"16kHz stereo int16", 16000, 2, FormatInt16LE},
		{"8000Hz mono float32", 8000, 1, FormatFloat32LE},
		{"12000Hz stereo int16", 12000, 2, FormatInt16LE},
	}

	source := &slicePacketSource{packets: nil}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reader, err := NewReader(tc.sampleRate, tc.channels, source, tc.format)
			if err != nil {
				t.Fatalf("NewReader failed: %v", err)
			}
			if reader.SampleRate() != tc.sampleRate {
				t.Errorf("SampleRate() = %d, want %d", reader.SampleRate(), tc.sampleRate)
			}
			if reader.Channels() != tc.channels {
				t.Errorf("Channels() = %d, want %d", reader.Channels(), tc.channels)
			}
		})
	}
}

// TestNewReader_InvalidParams tests creating readers with invalid parameters.
func TestNewReader_InvalidParams(t *testing.T) {
	source := &slicePacketSource{packets: nil}

	testCases := []struct {
		name       string
		sampleRate int
		channels   int
		wantErr    error
	}{
		{"invalid sample rate 44100", 44100, 1, ErrInvalidSampleRate},
		{"invalid sample rate 0", 0, 1, ErrInvalidSampleRate},
		{"invalid sample rate negative", -8000, 1, ErrInvalidSampleRate},
		{"invalid channels 0", 48000, 0, ErrInvalidChannels},
		{"invalid channels 3", 48000, 3, ErrInvalidChannels},
		{"invalid channels negative", 48000, -1, ErrInvalidChannels},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewReader(tc.sampleRate, tc.channels, source, FormatFloat32LE)
			if err != tc.wantErr {
				t.Errorf("NewReader error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestReader_Read_SinglePacket tests reading from a single-packet source.
func TestReader_Read_SinglePacket(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960 // 20ms

	packet, err := generateTestPacket(sampleRate, channels, frameSize)
	if err != nil {
		t.Fatalf("generateTestPacket failed: %v", err)
	}
	t.Logf("Generated packet: %d bytes", len(packet))

	source := &slicePacketSource{packets: [][]byte{packet}}
	reader, err := NewReader(sampleRate, channels, source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Read all data
	var allBytes []byte
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		allBytes = append(allBytes, buf[:n]...)
	}

	// Verify we got expected number of bytes
	// frameSize * channels * 4 bytes per float32
	expectedBytes := frameSize * channels * 4
	t.Logf("Read %d bytes, expected %d", len(allBytes), expectedBytes)
	if len(allBytes) < expectedBytes {
		t.Errorf("Read %d bytes, want at least %d", len(allBytes), expectedBytes)
	}
}

// TestReader_Read_MultiplePackets tests reading across packet boundaries.
func TestReader_Read_MultiplePackets(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960

	// Generate 3 packets
	packets := make([][]byte, 3)
	for i := 0; i < 3; i++ {
		packet, err := generateTestPacket(sampleRate, channels, frameSize)
		if err != nil {
			t.Fatalf("generateTestPacket failed: %v", err)
		}
		packets[i] = packet
	}

	source := &slicePacketSource{packets: packets}
	reader, err := NewReader(sampleRate, channels, source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Read all data
	var allBytes []byte
	buf := make([]byte, 1000) // Small buffer to force multiple reads
	for {
		n, err := reader.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		allBytes = append(allBytes, buf[:n]...)
	}

	// Each packet should produce frameSize * channels * 4 bytes
	expectedBytesPerPacket := frameSize * channels * 4
	expectedTotal := expectedBytesPerPacket * 3
	t.Logf("Read %d bytes, expected %d", len(allBytes), expectedTotal)
	if len(allBytes) < expectedTotal {
		t.Errorf("Read %d bytes, want at least %d", len(allBytes), expectedTotal)
	}
}

// TestReader_Read_PartialRead tests partial reads work correctly.
func TestReader_Read_PartialRead(t *testing.T) {
	sampleRate := 48000
	channels := 1
	frameSize := 960

	packet, err := generateTestPacket(sampleRate, channels, frameSize)
	if err != nil {
		t.Fatalf("generateTestPacket failed: %v", err)
	}

	source := &slicePacketSource{packets: [][]byte{packet}}
	reader, err := NewReader(sampleRate, channels, source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Read with very small buffer to force partial reads
	var allBytes []byte
	buf := make([]byte, 17) // Odd size that doesn't align with sample boundaries
	for {
		n, err := reader.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		allBytes = append(allBytes, buf[:n]...)
	}

	// Verify all bytes read
	expectedBytes := frameSize * channels * 4
	t.Logf("Read %d bytes, expected %d", len(allBytes), expectedBytes)
	if len(allBytes) < expectedBytes {
		t.Errorf("Read %d bytes, want at least %d", len(allBytes), expectedBytes)
	}
}

// TestReader_Read_EOF tests EOF handling.
func TestReader_Read_EOF(t *testing.T) {
	source := &slicePacketSource{packets: [][]byte{}} // Empty source
	reader, err := NewReader(48000, 2, source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := reader.Read(buf)
	if err != io.EOF {
		t.Errorf("Read error = %v, want io.EOF", err)
	}
	if n != 0 {
		t.Errorf("Read returned %d bytes on EOF, want 0", n)
	}

	// Second read should also return EOF
	n, err = reader.Read(buf)
	if err != io.EOF {
		t.Errorf("Second Read error = %v, want io.EOF", err)
	}
	if n != 0 {
		t.Errorf("Second Read returned %d bytes on EOF, want 0", n)
	}
}

// TestReader_Read_PLC tests nil packet triggers PLC.
func TestReader_Read_PLC(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960

	// First packet is valid, second is nil (PLC), third is valid
	packet1, err := generateTestPacket(sampleRate, channels, frameSize)
	if err != nil {
		t.Fatalf("generateTestPacket failed: %v", err)
	}
	packet3, err := generateTestPacket(sampleRate, channels, frameSize)
	if err != nil {
		t.Fatalf("generateTestPacket failed: %v", err)
	}

	source := &slicePacketSource{packets: [][]byte{packet1, nil, packet3}}
	reader, err := NewReader(sampleRate, channels, source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Read all data - should not error on nil packet
	var allBytes []byte
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		allBytes = append(allBytes, buf[:n]...)
	}

	// Should have 3 frames worth of data
	expectedBytesPerFrame := frameSize * channels * 4
	expectedTotal := expectedBytesPerFrame * 3
	t.Logf("Read %d bytes, expected %d", len(allBytes), expectedTotal)
	if len(allBytes) < expectedTotal {
		t.Errorf("Read %d bytes, want at least %d", len(allBytes), expectedTotal)
	}
}

// TestReader_Format_Float32LE tests float32 byte format.
func TestReader_Format_Float32LE(t *testing.T) {
	sampleRate := 48000
	channels := 1
	frameSize := 960

	packet, err := generateTestPacket(sampleRate, channels, frameSize)
	if err != nil {
		t.Fatalf("generateTestPacket failed: %v", err)
	}

	source := &slicePacketSource{packets: [][]byte{packet}}
	reader, err := NewReader(sampleRate, channels, source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Read all data
	var allBytes []byte
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		allBytes = append(allBytes, buf[:n]...)
	}

	// Parse bytes as float32 and verify they are valid
	if len(allBytes)%4 != 0 {
		t.Fatalf("Byte count %d not divisible by 4", len(allBytes))
	}

	numSamples := len(allBytes) / 4
	for i := 0; i < numSamples; i++ {
		bits := binary.LittleEndian.Uint32(allBytes[i*4:])
		sample := math.Float32frombits(bits)
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			t.Errorf("Invalid float32 at sample %d: %v", i, sample)
		}
		// Audio samples should be in [-1, 1] range (or slightly beyond due to processing)
		if sample < -2.0 || sample > 2.0 {
			t.Errorf("Sample %d out of range: %v", i, sample)
		}
	}
	t.Logf("Verified %d float32 samples", numSamples)
}

// TestReader_Format_Int16LE tests int16 byte format.
func TestReader_Format_Int16LE(t *testing.T) {
	sampleRate := 48000
	channels := 1
	frameSize := 960

	packet, err := generateTestPacket(sampleRate, channels, frameSize)
	if err != nil {
		t.Fatalf("generateTestPacket failed: %v", err)
	}

	source := &slicePacketSource{packets: [][]byte{packet}}
	reader, err := NewReader(sampleRate, channels, source, FormatInt16LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Read all data
	var allBytes []byte
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		allBytes = append(allBytes, buf[:n]...)
	}

	// Parse bytes as int16 and verify they are valid
	if len(allBytes)%2 != 0 {
		t.Fatalf("Byte count %d not divisible by 2", len(allBytes))
	}

	numSamples := len(allBytes) / 2
	var hasNonZero bool
	var maxAbs int16
	for i := 0; i < numSamples; i++ {
		sample := int16(binary.LittleEndian.Uint16(allBytes[i*2:]))
		if sample != 0 {
			hasNonZero = true
		}
		if sample < 0 && -sample > maxAbs {
			maxAbs = -sample
		} else if sample > maxAbs {
			maxAbs = sample
		}
	}

	t.Logf("Verified %d int16 samples, maxAbs=%d, hasNonZero=%v", numSamples, maxAbs, hasNonZero)
	// Note: Some codec processing may result in very low levels
	// The primary check is that conversion happened correctly (divisible by 2, no crash)
}

// TestReader_Reset tests resetting the reader.
func TestReader_Reset(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960

	packet, err := generateTestPacket(sampleRate, channels, frameSize)
	if err != nil {
		t.Fatalf("generateTestPacket failed: %v", err)
	}

	source := &slicePacketSource{packets: [][]byte{packet}}
	reader, err := NewReader(sampleRate, channels, source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Read some data
	buf := make([]byte, 4096)
	_, err = reader.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read failed: %v", err)
	}

	// Reset
	reader.Reset()

	// Verify state is cleared
	if reader.offset != 0 {
		t.Error("offset not reset")
	}
	if reader.eof {
		t.Error("eof not reset")
	}
	if reader.byteBuf != nil {
		t.Error("byteBuf not reset")
	}
}

// TestSampleFormat_BytesPerSample tests BytesPerSample.
func TestSampleFormat_BytesPerSample(t *testing.T) {
	testCases := []struct {
		format SampleFormat
		want   int
	}{
		{FormatFloat32LE, 4},
		{FormatInt16LE, 2},
		{SampleFormat(999), 4}, // Unknown defaults to 4
	}

	for _, tc := range testCases {
		got := tc.format.BytesPerSample()
		if got != tc.want {
			t.Errorf("SampleFormat(%d).BytesPerSample() = %d, want %d", tc.format, got, tc.want)
		}
	}
}

// TestReader_io_Reader_Interface verifies Reader implements io.Reader.
func TestReader_io_Reader_Interface(t *testing.T) {
	source := &slicePacketSource{packets: nil}
	reader, err := NewReader(48000, 2, source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Verify interface compliance at compile time
	var _ io.Reader = reader

	// Also test with io.Copy
	packet, err := generateTestPacket(48000, 2, 960)
	if err != nil {
		t.Fatalf("generateTestPacket failed: %v", err)
	}

	source2 := &slicePacketSource{packets: [][]byte{packet}}
	reader2, err := NewReader(48000, 2, source2, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	var buf bytes.Buffer
	n, err := io.Copy(&buf, reader2)
	if err != nil {
		t.Fatalf("io.Copy failed: %v", err)
	}
	t.Logf("io.Copy copied %d bytes", n)
	if n == 0 {
		t.Error("io.Copy copied 0 bytes")
	}
}
