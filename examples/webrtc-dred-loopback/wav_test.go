package main

import (
	"encoding/binary"
	"os"
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
