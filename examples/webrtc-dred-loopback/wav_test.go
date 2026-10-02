package main

import (
	"encoding/binary"
	"io"
	"os"
	"strings"
	"testing"
)

func TestWAVRecorderCreatesDistinctFilesWithoutOverwritingOpenRecording(t *testing.T) {
	dir := t.TempDir()
	first, err := newWAVRecorder(dir, 1, audioSampleRate)
	if err != nil {
		t.Fatalf("create first recorder: %v", err)
	}
	defer first.Close()
	if err := first.WriteFloat32([]float32{0.25}); err != nil {
		t.Fatalf("write first recording: %v", err)
	}

	second, err := newWAVRecorder(dir, 1, audioSampleRate)
	if err != nil {
		t.Fatalf("create second recorder: %v", err)
	}
	defer second.Close()
	if first.Path() == second.Path() {
		t.Fatalf("recorders share path %q", first.Path())
	}
	if err := second.WriteFloat32([]float32{-0.5}); err != nil {
		t.Fatalf("write second recording: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first recording: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second recording: %v", err)
	}

	firstData := readWAVRecording(t, first.Path())
	secondData := readWAVRecording(t, second.Path())
	if got := int16(binary.LittleEndian.Uint16(firstData[44:46])); got != 8192 {
		t.Fatalf("first recording sample=%d, want 8192", got)
	}
	if got := int16(binary.LittleEndian.Uint16(secondData[44:46])); got != -16384 {
		t.Fatalf("second recording sample=%d, want -16384", got)
	}
}

func TestWAVRecorderRejectsClassicRIFFOverflowAndReportsFailure(t *testing.T) {
	recorder, err := newWAVRecorder(t.TempDir(), audioChannels, audioSampleRate)
	if err != nil {
		t.Fatalf("create recorder: %v", err)
	}
	defer recorder.Close()

	maxDataBytes := maxWAVDataBytes(audioChannels)
	blockAlign := uint32(audioChannels * wavBytesPerSample)
	if maxDataBytes%blockAlign != 0 {
		t.Fatalf("maximum data size %d is not aligned to block size %d", maxDataBytes, blockAlign)
	}
	// Truncation creates a sparse recording with the same size and write
	// position as a live recorder at the classic RIFF limit.
	fileSize := int64(wavHeaderSize) + int64(maxDataBytes)
	if err := recorder.file.Truncate(fileSize); err != nil {
		t.Fatalf("create sparse boundary recording: %v", err)
	}
	if _, err := recorder.file.Seek(0, io.SeekEnd); err != nil {
		t.Fatalf("seek to recording end: %v", err)
	}
	recorder.dataBytes = maxDataBytes

	e := &engine{recorder: recorder}
	e.writeDecodedLocked([]float32{0.25, -0.25}, 1, decodeNormal)
	if !strings.HasPrefix(e.stats.DREDStatus, "recording failed:") || !strings.Contains(e.stats.DREDStatus, "classic RIFF data limit") {
		t.Fatalf("recording status = %q, want a reported classic RIFF limit failure", e.stats.DREDStatus)
	}
	if recorder.dataBytes != maxDataBytes {
		t.Fatalf("data bytes = %d after rejected frame, want %d", recorder.dataBytes, maxDataBytes)
	}
	info, err := recorder.file.Stat()
	if err != nil {
		t.Fatalf("stat recording: %v", err)
	}
	if info.Size() != fileSize {
		t.Fatalf("recording size = %d after rejected frame, want %d", info.Size(), fileSize)
	}

	if err := recorder.Close(); err != nil {
		t.Fatalf("close boundary recording: %v", err)
	}
	file, err := os.Open(recorder.Path())
	if err != nil {
		t.Fatalf("open boundary recording: %v", err)
	}
	defer file.Close()
	header := make([]byte, wavHeaderSize)
	if _, err := io.ReadFull(file, header); err != nil {
		t.Fatalf("read WAV header: %v", err)
	}
	if got, want := binary.LittleEndian.Uint32(header[4:8]), uint32(wavRIFFFixedSize)+maxDataBytes; got != want {
		t.Fatalf("RIFF size = %d, want %d", got, want)
	}
	if got := binary.LittleEndian.Uint32(header[40:44]); got != maxDataBytes {
		t.Fatalf("data size = %d, want %d", got, maxDataBytes)
	}
}

func readWAVRecording(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recording %q: %v", path, err)
	}
	if len(data) != 46 || string(data[:4]) != "RIFF" || binary.LittleEndian.Uint32(data[40:44]) != 2 {
		t.Fatalf("recording %q has invalid header or size: %d bytes", path, len(data))
	}
	return data
}
