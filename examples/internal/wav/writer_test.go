package wav

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriterWritesStereoPCMAndRIFFHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.wav")
	writer, err := NewWriter(path, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	samples := []float32{
		1.25, -1.25,
		float32(1.5 / 32768), float32(2.5 / 32768),
		float32(-1.5 / 32768), float32(-2.5 / 32768),
	}
	if err := writer.WriteSamples(samples); err != nil {
		t.Fatalf("WriteSamples: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) != headerSize+len(samples)*bytesPerSample {
		t.Fatalf("WAV length = %d, want %d", len(data), headerSize+len(samples)*bytesPerSample)
	}
	if string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || string(data[36:40]) != "data" {
		t.Fatalf("invalid WAV signatures: %q %q %q", data[:4], data[8:12], data[36:40])
	}
	if got, want := binary.LittleEndian.Uint32(data[4:8]), uint32(36+len(samples)*bytesPerSample); got != want {
		t.Fatalf("RIFF size = %d, want %d", got, want)
	}
	if got, want := binary.LittleEndian.Uint32(data[40:44]), uint32(len(samples)*bytesPerSample); got != want {
		t.Fatalf("data size = %d, want %d", got, want)
	}
	if got := binary.LittleEndian.Uint16(data[22:24]); got != 2 {
		t.Fatalf("channels = %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint32(data[24:28]); got != 48000 {
		t.Fatalf("sample rate = %d, want 48000", got)
	}
	if got := binary.LittleEndian.Uint32(data[28:32]); got != 192000 {
		t.Fatalf("byte rate = %d, want 192000", got)
	}
	if got := binary.LittleEndian.Uint16(data[32:34]); got != 4 {
		t.Fatalf("block alignment = %d, want 4", got)
	}
	if got := binary.LittleEndian.Uint16(data[34:36]); got != 16 {
		t.Fatalf("bits per sample = %d, want 16", got)
	}
	wantPCM := []int16{32767, -32768, 2, 2, -2, -2}
	for i, want := range wantPCM {
		got := int16(binary.LittleEndian.Uint16(data[headerSize+i*bytesPerSample:]))
		if got != want {
			t.Fatalf("PCM[%d] = %d, want %d", i, got, want)
		}
	}
}

func TestNewWriterValidatesHeaderFieldsBeforeCreatingFile(t *testing.T) {
	for _, test := range []struct {
		name       string
		sampleRate int
		channels   int
	}{
		{name: "zero sample rate", sampleRate: 0, channels: 1},
		{name: "block alignment overflow", sampleRate: 48000, channels: 32768},
		{name: "byte rate overflow", sampleRate: 1 << 30, channels: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "existing.wav")
			const sentinel = "existing output"
			if err := os.WriteFile(path, []byte(sentinel), 0600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := NewWriter(path, test.sampleRate, test.channels); err == nil {
				t.Fatal("NewWriter accepted header fields outside WAV representation")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if string(got) != sentinel {
				t.Fatalf("existing output changed to %q", got)
			}
		})
	}
}

func TestWriterRejectsRIFFOverflowBeforeWriting(t *testing.T) {
	const wantMaxDataBytes = uint64(1<<32 - 1 - 36 - 3) // largest complete stereo frame
	path := filepath.Join(t.TempDir(), "boundary.wav")
	writer, err := NewWriter(path, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	if writer.maxDataBytes != wantMaxDataBytes {
		t.Fatalf("maximum data size = %d, want %d", writer.maxDataBytes, wantMaxDataBytes)
	}
	fileSize := int64(headerSize) + int64(writer.maxDataBytes)
	if err := writer.file.Truncate(fileSize); err != nil {
		t.Fatalf("create sparse boundary file: %v", err)
	}
	if _, err := writer.file.Seek(0, io.SeekEnd); err != nil {
		t.Fatalf("seek to boundary: %v", err)
	}
	writer.dataBytes = writer.maxDataBytes
	position, err := writer.file.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatalf("current position: %v", err)
	}

	if err := writer.WriteSamples([]float32{0.25, -0.25}); !errors.Is(err, ErrRIFFSizeLimit) {
		t.Fatalf("WriteSamples error = %v, want %v", err, ErrRIFFSizeLimit)
	}
	if writer.dataBytes != writer.maxDataBytes {
		t.Fatalf("data bytes = %d after rejected frame, want %d", writer.dataBytes, writer.maxDataBytes)
	}
	info, err := writer.file.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != fileSize {
		t.Fatalf("file size = %d after rejected frame, want %d", info.Size(), fileSize)
	}
	if got, err := writer.file.Seek(0, io.SeekCurrent); err != nil || got != position {
		t.Fatalf("file position = %d, err=%v; want unchanged position %d", got, err, position)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	var header [headerSize]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatalf("read sparse WAV header: %v", err)
	}
	if got, want := binary.LittleEndian.Uint32(header[4:8]), uint32(36)+uint32(writer.maxDataBytes); got != want {
		t.Fatalf("RIFF size = %d, want %d", got, want)
	}
	if got, want := binary.LittleEndian.Uint32(header[40:44]), uint32(writer.maxDataBytes); got != want {
		t.Fatalf("data size = %d, want %d", got, want)
	}
	info, err = f.Stat()
	if err != nil {
		t.Fatalf("Stat after Close: %v", err)
	}
	if info.Size() != fileSize {
		t.Fatalf("file size after Close = %d, want %d", info.Size(), fileSize)
	}
}
