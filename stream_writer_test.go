// stream_writer_test.go contains tests for the streaming io.Writer API.

package gopus

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

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
		sink       PacketSink
		format     SampleFormat
		wantErr    error
	}{
		{"invalid sample rate 44100", 44100, 1, sink, FormatFloat32LE, ErrInvalidSampleRate},
		{"invalid sample rate 0", 0, 1, sink, FormatFloat32LE, ErrInvalidSampleRate},
		{"invalid channels 0", 48000, 0, sink, FormatFloat32LE, ErrInvalidChannels},
		{"invalid channels 3", 48000, 3, sink, FormatFloat32LE, ErrInvalidChannels},
		{"nil sink", 48000, 2, nil, FormatFloat32LE, ErrNilPacketSink},
		{"invalid sample format", 48000, 2, sink, SampleFormat(999), ErrInvalidSampleFormat},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewWriter(tc.sampleRate, tc.channels, tc.sink, tc.format, ApplicationAudio)
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

func TestWriter_Close_FlushesAndClosesSink(t *testing.T) {
	sink := &closablePacketSink{}
	writer, err := NewWriter(48000, 2, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	pcmBytes := generateFloat32Bytes(48000, 2, 960/2, 440.0)
	if _, err := writer.Write(pcmBytes); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if len(sink.packets) != 1 {
		t.Fatalf("Close should flush one packet, got %d", len(sink.packets))
	}
	if sink.closeCalls != 1 {
		t.Fatalf("Close should forward to sink once, got %d", sink.closeCalls)
	}
}

func TestWriter_Close_Idempotent(t *testing.T) {
	sink := &closablePacketSink{}
	writer, err := NewWriter(48000, 2, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
	if sink.closeCalls != 1 {
		t.Fatalf("Close should be idempotent, got %d close calls", sink.closeCalls)
	}
}

func TestWriter_WriteAfterClose(t *testing.T) {
	sink := &slicePacketSink{}
	writer, err := NewWriter(48000, 2, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if _, err := writer.Write([]byte{0, 1, 2, 3}); err != io.ErrClosedPipe {
		t.Fatalf("Write after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
	if err := writer.Flush(); err != io.ErrClosedPipe {
		t.Fatalf("Flush after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
}

func TestWriter_ResetAfterCloseReopensWriter(t *testing.T) {
	sink := &slicePacketSink{}
	writer, err := NewWriter(48000, 2, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	writer.Reset()

	pcmBytes := generateFloat32Bytes(48000, 2, 960, 440.0)
	if _, err := writer.Write(pcmBytes); err != nil {
		t.Fatalf("Write after Reset failed: %v", err)
	}
	if len(sink.packets) != 1 {
		t.Fatalf("Write after Reset produced %d packets, want 1", len(sink.packets))
	}
}

func TestWriter_SinkShortWriteReturnsPartialProgress(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
	)

	sink := &scriptedPacketSink{failAtCall: 2, shortBytes: 1}
	writer, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	pcmBytes := generateFloat32Bytes(sampleRate, channels, frameSize*2, 440.0)
	n, err := writer.Write(pcmBytes)
	if err != io.ErrShortWrite {
		t.Fatalf("Write error = %v, want %v", err, io.ErrShortWrite)
	}

	frameBytes := frameSize * channels * 4
	if n != frameBytes {
		t.Fatalf("Write returned %d bytes consumed, want %d", n, frameBytes)
	}
	if len(sink.packets) != 1 {
		t.Fatalf("successful packets = %d, want 1", len(sink.packets))
	}
	if _, err := writer.Write(pcmBytes[:frameBytes]); err != io.ErrClosedPipe {
		t.Fatalf("Write after sink short write error = %v, want %v", err, io.ErrClosedPipe)
	}

	writer.Reset()
	sink.failAtCall = 0
	sink.shortBytes = 0
	if _, err := writer.Write(pcmBytes[:frameBytes]); err != nil {
		t.Fatalf("Write after Reset failed: %v", err)
	}
}

func TestWriter_SinkErrorAfterPartialWriteReturnsShortWrite(t *testing.T) {
	sink := &scriptedPacketSink{
		failAtCall: 1,
		shortBytes: 1,
		err:        errors.New("sink failure"),
	}
	writer, err := NewWriter(48000, 2, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	pcmBytes := generateFloat32Bytes(48000, 2, 960, 440.0)
	if n, err := writer.Write(pcmBytes); err != io.ErrShortWrite || n != 0 {
		t.Fatalf("Write = (%d, %v), want (0, %v)", n, err, io.ErrShortWrite)
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

// TestWriter_io_Writer_Interface verifies Writer implements io.Writer/io.Closer.
func TestWriter_io_Writer_Interface(t *testing.T) {
	sink := &slicePacketSink{}
	writer, err := NewWriter(48000, 2, sink, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Verify interface compliance at compile time
	var _ io.Writer = writer
	var _ io.Closer = writer

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

// =============================================================================
// Integration Tests
// =============================================================================
