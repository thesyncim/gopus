package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/container/ogg"
)

func TestCreateOggFileWithShortDuration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.opus")
	if err := createOggFile(path, 0.1, 64000); err != nil {
		t.Fatalf("createOggFile: %v", err)
	}
	if err := readOggFile(path); err != nil {
		t.Fatalf("readOggFile: %v", err)
	}
}

func TestReadOggFileRejectsTruncatedPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truncated.opus")
	if err := createOggFile(path, 0.1, 64000); err != nil {
		t.Fatalf("createOggFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if err := os.Truncate(path, info.Size()-1); err != nil {
		t.Fatalf("truncate output: %v", err)
	}
	if err := readOggFile(path); err == nil {
		t.Fatal("readOggFile accepted a truncated Ogg page")
	}
}

func TestCreateOggFileRejectsLessThanOneFrame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "too-short.opus")
	if err := createOggFile(path, 0.01, 64000); err == nil {
		t.Fatal("createOggFile accepted a duration shorter than one frame")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("output exists after invalid duration: stat error = %v", err)
	}
}

func TestCreateOggFileRejectsUnrepresentableDurationBeforeCreatingOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "too-long.opus")
	if err := createOggFile(path, 1e30, 64000); err == nil {
		t.Fatal("createOggFile accepted an unrepresentable sample count")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("output exists after invalid duration: stat error = %v", err)
	}
}

func TestReadRejectsMalformedOpusPacket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid-packet.opus")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create output: %v", err)
	}
	writer, err := ogg.NewWriter(f, 48000, 1)
	if err != nil {
		_ = f.Close()
		t.Fatalf("create Ogg writer: %v", err)
	}
	if err := writer.WritePacket([]byte{0x03}, 960); err != nil {
		_ = f.Close()
		t.Fatalf("write malformed packet: %v", err)
	}
	if err := writer.Close(); err != nil {
		_ = f.Close()
		t.Fatalf("close Ogg writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close output: %v", err)
	}

	if err := readOggFile(path); err == nil || !strings.Contains(err.Error(), "decode packet 0:") {
		t.Fatalf("read malformed packet error = %v, want a decode packet error", err)
	}
}
