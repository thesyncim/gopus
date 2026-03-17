package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestDecodeWAVPCM16(t *testing.T) {
	t.Helper()

	data := buildTestWAV(t, 1, 2, 8000, 16, int16Payload(t, -32768, 0, 16384, 32767))

	samples, rate, channels, err := decodeWAV(data)
	if err != nil {
		t.Fatalf("decodeWAV returned error: %v", err)
	}
	if rate != 8000 {
		t.Fatalf("rate=%d want 8000", rate)
	}
	if channels != 2 {
		t.Fatalf("channels=%d want 2", channels)
	}

	want := []float32{-1.0, 0.0, 0.5, 32767.0 / 32768.0}
	assertFloat32Slice(t, samples, want)
}

func TestDecodeWAVFloat32(t *testing.T) {
	t.Helper()

	data := buildTestWAV(t, 3, 1, 16000, 32, float32Payload(t, -0.75, 0.25, 1.0))

	samples, rate, channels, err := decodeWAV(data)
	if err != nil {
		t.Fatalf("decodeWAV returned error: %v", err)
	}
	if rate != 16000 {
		t.Fatalf("rate=%d want 16000", rate)
	}
	if channels != 1 {
		t.Fatalf("channels=%d want 1", channels)
	}

	want := []float32{-0.75, 0.25, 1.0}
	assertFloat32Slice(t, samples, want)
}

func TestDecodeWAVRejectsUnsupportedFormat(t *testing.T) {
	t.Helper()

	data := buildTestWAV(t, 1, 1, 8000, 24, []byte{0x00, 0x01, 0x02})

	_, _, _, err := decodeWAV(data)
	if err == nil {
		t.Fatalf("expected unsupported format error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported wav format") {
		t.Fatalf("error=%q want unsupported wav format", err)
	}
}

func TestDecodeWAVRejectsMisalignedData(t *testing.T) {
	t.Helper()

	data := buildTestWAV(t, 3, 1, 8000, 32, []byte{0x00, 0x01, 0x02})

	_, _, _, err := decodeWAV(data)
	if err == nil {
		t.Fatalf("expected misaligned data error, got nil")
	}
	if !strings.Contains(err.Error(), "32-bit aligned") {
		t.Fatalf("error=%q want 32-bit aligned", err)
	}
}

func buildTestWAV(t *testing.T, audioFormat, channels uint16, sampleRate uint32, bitsPerSample uint16, pcmData []byte) []byte {
	t.Helper()

	var fmtChunk bytes.Buffer
	if err := binary.Write(&fmtChunk, binary.LittleEndian, audioFormat); err != nil {
		t.Fatalf("write audio format: %v", err)
	}
	if err := binary.Write(&fmtChunk, binary.LittleEndian, channels); err != nil {
		t.Fatalf("write channels: %v", err)
	}
	if err := binary.Write(&fmtChunk, binary.LittleEndian, sampleRate); err != nil {
		t.Fatalf("write sample rate: %v", err)
	}

	blockAlign := channels * (bitsPerSample / 8)
	byteRate := sampleRate * uint32(blockAlign)
	if err := binary.Write(&fmtChunk, binary.LittleEndian, byteRate); err != nil {
		t.Fatalf("write byte rate: %v", err)
	}
	if err := binary.Write(&fmtChunk, binary.LittleEndian, blockAlign); err != nil {
		t.Fatalf("write block align: %v", err)
	}
	if err := binary.Write(&fmtChunk, binary.LittleEndian, bitsPerSample); err != nil {
		t.Fatalf("write bits per sample: %v", err)
	}

	var dataChunk bytes.Buffer
	dataChunk.WriteString("data")
	if err := binary.Write(&dataChunk, binary.LittleEndian, uint32(len(pcmData))); err != nil {
		t.Fatalf("write data chunk size: %v", err)
	}
	if _, err := dataChunk.Write(pcmData); err != nil {
		t.Fatalf("write pcm data: %v", err)
	}
	if len(pcmData)%2 == 1 {
		if err := dataChunk.WriteByte(0); err != nil {
			t.Fatalf("write data pad byte: %v", err)
		}
	}

	var riff bytes.Buffer
	riff.WriteString("RIFF")
	riffSize := uint32(4 + 8 + fmtChunk.Len() + dataChunk.Len())
	if err := binary.Write(&riff, binary.LittleEndian, riffSize); err != nil {
		t.Fatalf("write riff size: %v", err)
	}
	riff.WriteString("WAVE")
	riff.WriteString("fmt ")
	if err := binary.Write(&riff, binary.LittleEndian, uint32(fmtChunk.Len())); err != nil {
		t.Fatalf("write fmt chunk size: %v", err)
	}
	if _, err := riff.Write(fmtChunk.Bytes()); err != nil {
		t.Fatalf("write fmt chunk: %v", err)
	}
	if _, err := riff.Write(dataChunk.Bytes()); err != nil {
		t.Fatalf("write data chunk: %v", err)
	}
	return riff.Bytes()
}

func int16Payload(t *testing.T, samples ...int16) []byte {
	t.Helper()

	var payload bytes.Buffer
	for _, sample := range samples {
		if err := binary.Write(&payload, binary.LittleEndian, sample); err != nil {
			t.Fatalf("write int16 payload: %v", err)
		}
	}
	return payload.Bytes()
}

func float32Payload(t *testing.T, samples ...float32) []byte {
	t.Helper()

	var payload bytes.Buffer
	for _, sample := range samples {
		if err := binary.Write(&payload, binary.LittleEndian, sample); err != nil {
			t.Fatalf("write float32 payload: %v", err)
		}
	}
	return payload.Bytes()
}
