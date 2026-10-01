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

func TestCreateOggFileWithShortDuration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.opus")
	if err := createOggFile(path, 0.1, 64000); err != nil {
		t.Fatalf("createOggFile: %v", err)
	}
	assertPlayableSamples(t, path, 4800)
	if err := readOggFile(path); err != nil {
		t.Fatalf("readOggFile: %v", err)
	}
}

func TestCreateOggFilePreservesPartialAndSubframeDurations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration float64
		samples  int
	}{
		{name: "partial frame", duration: 0.105, samples: 5040},
		{name: "shorter than one frame", duration: 0.005, samples: 240},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "duration.opus")
			if err := createOggFile(path, tc.duration, 64000); err != nil {
				t.Fatalf("createOggFile: %v", err)
			}
			assertPlayableSamples(t, path, tc.samples)
		})
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

func TestCreateOggFileRejectsInvalidOrSubsampleDurations(t *testing.T) {
	for _, duration := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1), 1e-9} {
		t.Run(fmt.Sprintf("duration_%g", duration), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.opus")
			if err := createOggFile(path, duration, 64000); err == nil {
				t.Fatalf("createOggFile accepted duration %g", duration)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("output exists after invalid duration: stat error = %v", err)
			}
		})
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
