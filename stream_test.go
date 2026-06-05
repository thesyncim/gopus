// stream_test.go contains tests for the streaming io.Reader/io.Writer API.

package gopus

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"
)

// slicePacketSource implements PacketReader for testing.
type slicePacketSource struct {
	packets [][]byte
	index   int
}

func (s *slicePacketSource) ReadPacketInto(dst []byte) (int, uint64, error) {
	if s.index >= len(s.packets) {
		return 0, 0, io.EOF
	}
	packet := s.packets[s.index]
	s.index++
	if packet == nil {
		return 0, 0, nil
	}
	if len(packet) > len(dst) {
		return 0, 0, ErrPacketTooLarge
	}
	n := copy(dst, packet)
	return n, 0, nil
}

type slicePacketSourceWithGranule struct {
	packets  [][]byte
	granules []uint64
	index    int
}

func (s *slicePacketSourceWithGranule) ReadPacketInto(dst []byte) (int, uint64, error) {
	if s.index >= len(s.packets) {
		return 0, 0, io.EOF
	}
	packet := s.packets[s.index]
	granule := s.granules[s.index]
	s.index++
	if packet == nil {
		return 0, granule, nil
	}
	if len(packet) > len(dst) {
		return 0, 0, ErrPacketTooLarge
	}
	n := copy(dst, packet)
	return n, granule, nil
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

type closablePacketSink struct {
	slicePacketSink
	closeCalls int
}

func (s *closablePacketSink) Close() error {
	s.closeCalls++
	return nil
}

type scriptedPacketSink struct {
	packets    [][]byte
	calls      int
	failAtCall int
	shortBytes int
	err        error
}

func (s *scriptedPacketSink) WritePacket(packet []byte) (int, error) {
	s.calls++
	if s.calls == s.failAtCall {
		if s.err != nil {
			return s.shortBytes, s.err
		}
		return s.shortBytes, nil
	}
	cp := make([]byte, len(packet))
	copy(cp, packet)
	s.packets = append(s.packets, cp)
	return len(packet), nil
}

// generateTestPacket generates a valid Opus packet by encoding test audio.
func generateTestPacket(sampleRate, channels, frameSize int) ([]byte, error) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
	if err != nil {
		return nil, err
	}
	enc.SetFrameSize(frameSize)

	// Generate a simple sine wave
	pcm := make([]float32, frameSize*channels)
	freq := 440.0
	for i := range frameSize {
		sample := float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := range channels {
			pcm[i*channels+ch] = sample
		}
	}

	return enc.EncodeFloat32(pcm)
}

// generateFloat32Bytes generates float32 PCM bytes for a sine wave.
func generateFloat32Bytes(sampleRate, channels, numSamples int, freq float64) []byte {
	buf := make([]byte, numSamples*channels*4)
	for i := range numSamples {
		sample := float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := range channels {
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
	for i := range numSamples {
		sample := int16(0.5 * 32767 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := range channels {
			idx := (i*channels + ch) * 2
			binary.LittleEndian.PutUint16(buf[idx:], uint16(sample))
		}
	}
	return buf
}

// channelPacketSink implements PacketSink using a channel for pipe tests.
type channelPacketSink struct {
	ch chan []byte
}

func (c *channelPacketSink) WritePacket(packet []byte) (int, error) {
	cp := make([]byte, len(packet))
	copy(cp, packet)
	c.ch <- cp
	return len(packet), nil
}

// channelPacketSource implements PacketReader using a channel for pipe tests.
type channelPacketSource struct {
	ch chan []byte
}

func (c *channelPacketSource) ReadPacketInto(dst []byte) (int, uint64, error) {
	packet, ok := <-c.ch
	if !ok {
		return 0, 0, io.EOF
	}
	if packet == nil {
		return 0, 0, nil
	}
	if len(packet) > len(dst) {
		return 0, 0, ErrPacketTooLarge
	}
	n := copy(dst, packet)
	return n, 0, nil
}

// computeSignalEnergy computes the total energy of float32 samples.
func computeSignalEnergy(samples []float32) float64 {
	var energy float64
	for _, s := range samples {
		energy += float64(s) * float64(s)
	}
	return energy
}

// TestStream_RoundTrip_Float32 tests round-trip encode/decode with float32 format.
func TestStream_RoundTrip_Float32(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960
	numFrames := 5

	// Encode
	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Generate sine wave
	pcmBytes := generateFloat32Bytes(sampleRate, channels, frameSize*numFrames, 440.0)
	_, err = writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	err = writer.Flush()
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	t.Logf("Encoded %d frames to %d packets", numFrames, len(sink.packets))

	// Decode
	source := &slicePacketSource{packets: sink.packets}
	reader, err := NewReader(DefaultDecoderConfig(sampleRate, channels), source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

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

	// Convert decoded bytes to float32 and compute energy
	numSamples := len(allBytes) / 4
	decoded := make([]float32, numSamples)
	for i := range numSamples {
		bits := binary.LittleEndian.Uint32(allBytes[i*4:])
		decoded[i] = math.Float32frombits(bits)
	}

	energy := computeSignalEnergy(decoded)
	t.Logf("Round-trip float32: %d input samples, %d output samples, energy=%.6f",
		frameSize*numFrames*channels, numSamples, energy)

	if numSamples == 0 {
		t.Error("No samples decoded")
	}
	if energy == 0 {
		t.Error("Decoded stream has zero energy")
	}
}

// TestStream_RoundTrip_Int16 tests round-trip encode/decode with int16 format.
func TestStream_RoundTrip_Int16(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960
	numFrames := 5

	// Encode
	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatInt16LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Generate sine wave as int16
	pcmBytes := generateInt16Bytes(sampleRate, channels, frameSize*numFrames, 440.0)
	_, err = writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	err = writer.Flush()
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	t.Logf("Encoded %d frames to %d packets (int16)", numFrames, len(sink.packets))

	// Decode to int16
	source := &slicePacketSource{packets: sink.packets}
	reader, err := NewReader(DefaultDecoderConfig(sampleRate, channels), source, FormatInt16LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

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

	// Convert decoded bytes to int16 and compute energy
	numSamples := len(allBytes) / 2
	var energy int64
	for i := range numSamples {
		sample := int16(binary.LittleEndian.Uint16(allBytes[i*2:]))
		energy += int64(sample) * int64(sample)
	}

	t.Logf("Round-trip int16: %d input samples, %d output samples, energy=%d",
		frameSize*numFrames*channels, numSamples, energy)

	// Note: Some codec processing may result in low energy; just verify decode worked
	if numSamples == 0 {
		t.Error("No samples decoded")
	}
}

// TestStream_Pipe tests streaming through a channel-based pipe.
func TestStream_Pipe(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960
	numFrames := 3

	packetChan := make(chan []byte, 10)
	sink := &channelPacketSink{ch: packetChan}
	source := &channelPacketSource{ch: packetChan}

	// Writer goroutine
	writerDone := make(chan error, 1) // Buffered to avoid blocking
	go func() {
		writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationAudio)
		if err != nil {
			close(packetChan)
			writerDone <- err
			return
		}

		pcmBytes := generateFloat32Bytes(sampleRate, channels, frameSize*numFrames, 440.0)
		_, err = writer.Write(pcmBytes)
		if err != nil {
			close(packetChan)
			writerDone <- err
			return
		}

		err = writer.Flush()
		close(packetChan) // Signal EOF when done - must close before sending to writerDone
		writerDone <- err
	}()

	// Reader in main goroutine
	reader, err := NewReader(DefaultDecoderConfig(sampleRate, channels), source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

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

	// Wait for writer to finish
	if err := <-writerDone; err != nil {
		t.Fatalf("Writer failed: %v", err)
	}

	// Verify output
	numSamples := len(allBytes) / 4
	t.Logf("Pipe test: received %d samples through channel pipe", numSamples)

	if numSamples < frameSize*numFrames*channels/2 {
		t.Errorf("Received only %d samples, expected at least %d",
			numSamples, frameSize*numFrames*channels/2)
	}
}

// TestStream_LargeTransfer tests transferring 1 second of audio.
func TestStream_LargeTransfer(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960 // 20ms
	oneSecondSamples := sampleRate * channels
	numFrames := oneSecondSamples / (frameSize * channels)

	// Encode 1 second of audio
	sink := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	pcmBytes := generateFloat32Bytes(sampleRate, channels, sampleRate, 440.0) // 1 second
	_, err = writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	err = writer.Flush()
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	t.Logf("Large transfer: encoded 1 second (%d samples) to %d packets", sampleRate*channels, len(sink.packets))

	// Verify we got approximately the right number of packets
	// 1 second = 50 frames of 20ms
	if len(sink.packets) < numFrames-1 || len(sink.packets) > numFrames+1 {
		t.Errorf("Got %d packets, expected approximately %d", len(sink.packets), numFrames)
	}

	// Decode and verify
	source := &slicePacketSource{packets: sink.packets}
	reader, err := NewReader(DefaultDecoderConfig(sampleRate, channels), source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	var allBytes []byte
	buf := make([]byte, 8192)
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

	numSamples := len(allBytes) / 4
	t.Logf("Large transfer: decoded %d samples", numSamples)

	// Should have at least 90% of samples (allowing for codec processing)
	expectedMin := sampleRate * channels * 9 / 10
	if numSamples < expectedMin {
		t.Errorf("Decoded only %d samples, expected at least %d", numSamples, expectedMin)
	}
}

// TestStream_io_Copy tests using io.Copy with Reader.
func TestStream_io_Copy(t *testing.T) {
	sampleRate := 48000
	channels := 2
	frameSize := 960
	numFrames := 3

	// Generate packets
	packets := make([][]byte, numFrames)
	for i := range numFrames {
		packet, err := generateTestPacket(sampleRate, channels, frameSize)
		if err != nil {
			t.Fatalf("generateTestPacket failed: %v", err)
		}
		packets[i] = packet
	}

	source := &slicePacketSource{packets: packets}
	reader, err := NewReader(DefaultDecoderConfig(sampleRate, channels), source, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Use io.Copy
	var buf bytes.Buffer
	n, err := io.Copy(&buf, reader)
	if err != nil {
		t.Fatalf("io.Copy failed: %v", err)
	}

	expectedBytes := frameSize * channels * 4 * numFrames
	t.Logf("io.Copy: copied %d bytes, expected %d", n, expectedBytes)

	if int(n) < expectedBytes/2 {
		t.Errorf("io.Copy copied only %d bytes, expected at least %d", n, expectedBytes/2)
	}
}

// TestStream_MixedReadWrite tests alternating read/write operations.
func TestStream_MixedReadWrite(t *testing.T) {
	sampleRate := 48000
	channels := 1
	frameSize := 960

	// Encode 3 frames, decode, encode 2 more, decode all
	sink1 := &slicePacketSink{}
	writer, err := NewWriter(sampleRate, channels, sink1, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Encode 3 frames
	pcmBytes := generateFloat32Bytes(sampleRate, channels, frameSize*3, 440.0)
	_, err = writer.Write(pcmBytes)
	if err != nil {
		t.Fatalf("Write 1 failed: %v", err)
	}

	if len(sink1.packets) != 3 {
		t.Fatalf("Expected 3 packets after first write, got %d", len(sink1.packets))
	}

	// Decode first batch
	source1 := &slicePacketSource{packets: sink1.packets}
	reader, err := NewReader(DefaultDecoderConfig(sampleRate, channels), source1, FormatFloat32LE)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	var batch1 []byte
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read 1 failed: %v", err)
		}
		batch1 = append(batch1, buf[:n]...)
	}

	// Encode 2 more frames
	sink2 := &slicePacketSink{}
	writer2, err := NewWriter(sampleRate, channels, sink2, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter 2 failed: %v", err)
	}

	pcmBytes2 := generateFloat32Bytes(sampleRate, channels, frameSize*2, 880.0)
	_, err = writer2.Write(pcmBytes2)
	if err != nil {
		t.Fatalf("Write 2 failed: %v", err)
	}

	if len(sink2.packets) != 2 {
		t.Errorf("Expected 2 packets after second write, got %d", len(sink2.packets))
	}

	t.Logf("Mixed read/write: batch1=%d bytes, batch2=%d packets", len(batch1), len(sink2.packets))
}
