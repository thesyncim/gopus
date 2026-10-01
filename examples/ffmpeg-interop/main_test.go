package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/container/ogg"
)

func TestDecodeOpusFileRejectsTruncatedPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truncated.opus")
	if err := encodeTestSignal(path, 0.1); err != nil {
		t.Fatalf("encodeTestSignal: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if err := os.Truncate(path, info.Size()-1); err != nil {
		t.Fatalf("truncate output: %v", err)
	}
	if err := decodeOpusFile(path); err == nil {
		t.Fatal("decodeOpusFile accepted a truncated Ogg page")
	}
}

func TestEncodeTestSignalPreservesPlayableDuration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration float64
		samples  int
	}{
		{name: "aligned frame", duration: 0.1, samples: 4800},
		{name: "partial frame", duration: 0.105, samples: 5040},
		{name: "shorter than one frame", duration: 0.005, samples: 240},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "duration.opus")
			if err := encodeTestSignal(path, tc.duration); err != nil {
				t.Fatalf("encodeTestSignal: %v", err)
			}
			assertPlayableSamples(t, path, tc.samples)
		})
	}
}

func TestEncodeTestSignalRejectsInvalidOrSubsampleDurations(t *testing.T) {
	for _, duration := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1), 1e-9} {
		t.Run(fmt.Sprintf("duration_%g", duration), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.opus")
			if err := encodeTestSignal(path, duration); err == nil {
				t.Fatalf("encodeTestSignal accepted duration %g", duration)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("output exists after invalid duration: stat error = %v", err)
			}
		})
	}
}

func TestEncodeTestSignalRejectsUnrepresentableDurationBeforeCreatingOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "too-long.opus")
	if err := encodeTestSignal(path, 1e30); err == nil {
		t.Fatal("encodeTestSignal accepted an unrepresentable sample count")
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

	if err := decodeOpusFile(path); err == nil || !strings.Contains(err.Error(), "decode packet 0:") {
		t.Fatalf("read malformed packet error = %v, want a decode packet error", err)
	}
}

func assertPlayableSamples(t *testing.T, path string, want int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer f.Close()

	reader, err := ogg.NewReader(f)
	if err != nil {
		t.Fatalf("create Ogg reader: %v", err)
	}
	preSkip := uint64(reader.PreSkip())
	var finalGranule uint64
	for {
		_, granule, err := reader.ReadPacket()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read Ogg packet: %v", err)
		}
		finalGranule = granule
	}
	if finalGranule < preSkip {
		t.Fatalf("final granule %d precedes pre-skip %d", finalGranule, preSkip)
	}
	if got := int(finalGranule - preSkip); got != want {
		t.Fatalf("playable samples = %d (final granule %d - pre-skip %d), want %d", got, finalGranule, preSkip, want)
	}
}
