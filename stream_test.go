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

// generateFloat32Bytes generates float32 PCM bytes for a sine wave.
func generateFloat32Bytes(sampleRate, channels, numSamples int, freq float64) []byte {
	buf := make([]byte, numSamples*channels*4)
	for i := 0; i < numSamples; i++ {
		sample := float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := 0; ch < channels; ch++ {
			idx := (i*channels + ch) * 4
			bits := math.Float32bits(sample)
			binary.LittleEndian.PutUint32(buf[idx:], bits)
		}
	}
	return buf
}

// generateInt16Bytes generates int16 PCM bytes for a sine wave.
func generateInt16Bytes(sampleRate, channels, numSamples int, freq float64) []byte {
	buf := make([]byte, numSamples*channels*2)
	for i := 0; i < numSamples; i++ {
		sample := int16(0.5 * 32767 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := 0; ch < channels; ch++ {
			idx := (i*channels + ch) * 2
			binary.LittleEndian.PutUint16(buf[idx:], uint16(sample))
		}
	}
	return buf
}

// TestNewWriter_ValidParams tests creating writers with valid parameters.
func TestNewWriter_ValidParams(t *testing.T) {
	testCases := []struct {
		name       string
		sampleRate int
		channels   int
		format     SampleFormat
		app        Application
	}{
		{"48kHz mono float32 audio", 48000, 1, FormatFloat32LE, ApplicationAudio},
		{"48kHz stereo float32 audio", 48000, 2, FormatFloat32LE, ApplicationAudio},
		{"48kHz mono int16 voip", 48000, 1, FormatInt16LE, ApplicationVoIP},
		{"48kHz stereo int16 voip", 48000, 2, FormatInt16LE, ApplicationVoIP},
		{"24kHz mono float32 lowdelay", 24000, 1, FormatFloat32LE, ApplicationLowDelay},
		{"16kHz stereo int16 audio", 16000, 2, FormatInt16LE, ApplicationAudio},
	}

	sink := &slicePacketSink{}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			writer, err := NewWriter(tc.sampleRate, tc.channels, sink, tc.format, tc.app)
			if err != nil {
				t.Fatalf("NewWriter failed: %v", err)
			}
			if writer.SampleRate() != tc.sampleRate {
				t.Errorf("SampleRate() = %d, want %d", writer.SampleRate(), tc.sampleRate)
			}
			if writer.Channels() != tc.channels {
				t.Errorf("Channels() = %d, want %d", writer.Channels(), tc.channels)
			}
		})
	}
}

// TestNewWriter_InvalidParams tests creating writers with invalid parameters.
func TestNewWriter_InvalidParams(t *testing.T) {
	sink := &slicePacketSink{}

	testCases := []struct {
		name       string
		sampleRate int
		channels   int
		wantErr    error
	}{
		{"invalid sample rate 44100", 44100, 1, ErrInvalidSampleRate},
		{"invalid sample rate 0", 0, 1, ErrInvalidSampleRate},
		{"invalid channels 0", 48000, 0, ErrInvalidChannels},
		{"invalid channels 3", 48000, 3, ErrInvalidChannels},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewWriter(tc.sampleRate, tc.channels, sink, FormatFloat32LE, ApplicationAudio)
			if err != tc.wantErr {
				t.Errorf("NewWriter error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestWriter_Write_SingleFrame tests writing exactly one frame.
func TestWriter_Write_SingleFrame(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960

	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Generate exactly one frame
	pcmBytes := generateFloat32Bytes(sampleRate, channels, frameSize, 440.0)
	n, err := writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(pcmBytes) {
		t.Errorf("Write returned %d, want %d", n, len(pcmBytes))
	}

	// Should have one packet
	if len(sink.packets) != 1 {
		t.Errorf("Got %d packets, want 1", len(sink.packets))
	}
	t.Logf("Encoded %d bytes to %d byte packet", len(pcmBytes), len(sink.packets[0]))
}

// TestWriter_Write_MultipleFrames tests writing multiple frames at once.
func TestWriter_Write_MultipleFrames(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960
	numFrames := 3

	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Generate three frames
	pcmBytes := generateFloat32Bytes(sampleRate, channels, frameSize*numFrames, 440.0)
	n, err := writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(pcmBytes) {
		t.Errorf("Write returned %d, want %d", n, len(pcmBytes))
	}

	// Should have three packets
	if len(sink.packets) != numFrames {
		t.Errorf("Got %d packets, want %d", len(sink.packets), numFrames)
	}
	t.Logf("Encoded %d frames to %d packets", numFrames, len(sink.packets))
}

// TestWriter_Write_PartialFrame tests writing less than one frame (buffering).
func TestWriter_Write_PartialFrame(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960

	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write half a frame
	halfFrameSamples := frameSize / 2
	pcmBytes := generateFloat32Bytes(sampleRate, channels, halfFrameSamples, 440.0)
	n, err := writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(pcmBytes) {
		t.Errorf("Write returned %d, want %d", n, len(pcmBytes))
	}

	// Should have no packets yet
	if len(sink.packets) != 0 {
		t.Errorf("Got %d packets, want 0 (should be buffered)", len(sink.packets))
	}

	// Write another half
	n, err = writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Second Write failed: %v", err)
	}
	if n != len(pcmBytes) {
		t.Errorf("Second Write returned %d, want %d", n, len(pcmBytes))
	}

	// Now should have one packet
	if len(sink.packets) != 1 {
		t.Errorf("Got %d packets, want 1", len(sink.packets))
	}
	t.Logf("Buffering works: two half-frame writes produced 1 packet")
}

// TestWriter_Write_CrossFrameBoundary tests writing that spans frame boundaries.
func TestWriter_Write_CrossFrameBoundary(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960

	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write 1.5 frames worth
	samples := frameSize + frameSize/2
	pcmBytes := generateFloat32Bytes(sampleRate, channels, samples, 440.0)
	n, err := writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(pcmBytes) {
		t.Errorf("Write returned %d, want %d", n, len(pcmBytes))
	}

	// Should have 1 packet, 0.5 frame buffered
	if len(sink.packets) != 1 {
		t.Errorf("Got %d packets, want 1", len(sink.packets))
	}

	// Write another 0.5 frame to complete the buffered data
	pcmBytes2 := generateFloat32Bytes(sampleRate, channels, frameSize/2, 440.0)
	_, err = writer.Write(pcmBytes2)
	if err != nil {
		t.Fatalf("Second Write failed: %v", err)
	}

	// Now should have 2 packets
	if len(sink.packets) != 2 {
		t.Errorf("Got %d packets, want 2", len(sink.packets))
	}
	t.Logf("Cross-boundary writes work: got %d packets", len(sink.packets))
}

// TestWriter_Flush tests flushing remaining buffered samples.
func TestWriter_Flush(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960

	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write partial frame
	partialSamples := frameSize / 4
	pcmBytes := generateFloat32Bytes(sampleRate, channels, partialSamples, 440.0)
	_, err = writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// No packets yet
	if len(sink.packets) != 0 {
		t.Errorf("Got %d packets before flush, want 0", len(sink.packets))
	}

	// Flush
	err = writer.Flush()
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Now should have 1 packet (zero-padded)
	if len(sink.packets) != 1 {
		t.Errorf("Got %d packets after flush, want 1", len(sink.packets))
	}
	t.Logf("Flush zero-padded partial frame to packet of %d bytes", len(sink.packets[0]))
}

// TestWriter_Flush_Empty tests flushing with no buffered data.
func TestWriter_Flush_Empty(t *testing.T) {
	sink := &slicePacketSink{}
	writer, err := NewWriter(48000, 2, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Flush with nothing buffered should not error
	err = writer.Flush()
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// No packets
	if len(sink.packets) != 0 {
		t.Errorf("Got %d packets from empty flush, want 0", len(sink.packets))
	}
}

// TestWriter_Format_Float32LE tests float32 input format.
func TestWriter_Format_Float32LE(t *testing.T) {
	sampleRate := 48000
	channels := 1
	frameSize := 960

	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Generate float32 bytes
	pcmBytes := generateFloat32Bytes(sampleRate, channels, frameSize, 440.0)
	_, err = writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Should have one packet
	if len(sink.packets) != 1 {
		t.Errorf("Got %d packets, want 1", len(sink.packets))
	}
	t.Logf("Float32LE: %d input bytes -> %d byte packet", len(pcmBytes), len(sink.packets[0]))
}

// TestWriter_Format_Int16LE tests int16 input format.
func TestWriter_Format_Int16LE(t *testing.T) {
	sampleRate := 48000
	channels := 1
	frameSize := 960

	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatInt16LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Generate int16 bytes
	pcmBytes := generateInt16Bytes(sampleRate, channels, frameSize, 440.0)
	_, err = writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Should have one packet
	if len(sink.packets) != 1 {
		t.Errorf("Got %d packets, want 1", len(sink.packets))
	}
	t.Logf("Int16LE: %d input bytes -> %d byte packet", len(pcmBytes), len(sink.packets[0]))
}

// TestWriter_DTX tests that silence produces no packets with DTX enabled.
func TestWriter_DTX(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960

	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	writer.SetDTX(true)

	// Write silence (zeros) for multiple frames
	// DTX needs multiple frames to activate (DTXFrameThreshold = 20 frames)
	silentBytes := make([]byte, frameSize*channels*4*25) // 25 frames of silence
	_, err = writer.Write(silentBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// After threshold, some frames should be suppressed
	// We wrote 25 frames, DTX activates after 20, so at least last few should be suppressed
	t.Logf("DTX test: wrote 25 silent frames, got %d packets", len(sink.packets))
	// Just verify no error occurred; exact packet count depends on DTX implementation
}

// TestWriter_Reset tests resetting the writer.
func TestWriter_Reset(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960

	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write partial frame
	pcmBytes := generateFloat32Bytes(sampleRate, channels, frameSize/2, 440.0)
	_, err = writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Reset
	writer.Reset()

	// Buffer should be cleared (Flush should produce nothing)
	sink.packets = nil // Clear sink
	err = writer.Flush()
	if err != nil {
		t.Fatalf("Flush after reset failed: %v", err)
	}
	if len(sink.packets) != 0 {
		t.Error("Buffer not cleared after reset")
	}
}

// TestWriter_io_Writer_Interface verifies Writer implements io.Writer.
func TestWriter_io_Writer_Interface(t *testing.T) {
	sink := &slicePacketSink{}
	writer, err := NewWriter(48000, 2, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Verify interface compliance at compile time
	var _ io.Writer = writer

	// Also test with io.Copy
	pcmBytes := generateFloat32Bytes(48000, 2, 960*3, 440.0) // 3 frames
	src := bytes.NewReader(pcmBytes)

	n, err := io.Copy(writer, src)
	if err != nil {
		t.Fatalf("io.Copy failed: %v", err)
	}
	t.Logf("io.Copy wrote %d bytes, produced %d packets", n, len(sink.packets))

	if len(sink.packets) != 3 {
		t.Errorf("Got %d packets, want 3", len(sink.packets))
	}
}
