package main

import (
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

func TestEncodeToOggPreservesNonFrameAlignedDuration(t *testing.T) {
	const duration = 0.137
	path := filepath.Join(t.TempDir(), "non-frame-aligned.opus")
	stats, err := encodeToOgg(path, duration, 64000, 1, 960, gopus.ApplicationAudio, "sine")
	if err != nil {
		t.Fatalf("encodeToOgg: %v", err)
	}

	wantSamples := int(math.Round(duration * sampleRate))
	if want := float64(wantSamples) / sampleRate; stats.actualDuration != want {
		t.Fatalf("actual duration = %.9f, want %.9f", stats.actualDuration, want)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer f.Close()

	reader, err := ogg.NewReader(f)
	if err != nil {
		t.Fatalf("create Ogg reader: %v", err)
	}
	preSkip := int(reader.PreSkip())
	var packets int
	var finalGranule uint64
	for {
		_, granule, err := reader.ReadPacket()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read packet %d: %v", packets, err)
		}
		packets++
		finalGranule = granule
	}

	if packets != stats.frames {
		t.Fatalf("encoded packets = %d, stats report %d", packets, stats.frames)
	}
	if gotPackets, gotBytes, err := countEncodedOggPackets(path); err != nil {
		t.Fatalf("countEncodedOggPackets: %v", err)
	} else if gotPackets != stats.frames || gotBytes != stats.encodedBytes {
		t.Fatalf("parsed encoded stats = %d packets/%d payload bytes, want %d/%d", gotPackets, gotBytes, stats.frames, stats.encodedBytes)
	}
	if want := uint64(preSkip + wantSamples); finalGranule != want {
		t.Fatalf("final granule = %d, want pre-skip + input samples = %d", finalGranule, want)
	}
}

func TestCountEncodedOggPacketsRejectsInvalidStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.opus")
	if err := os.WriteFile(path, []byte("not an Ogg stream"), 0600); err != nil {
		t.Fatalf("write invalid input: %v", err)
	}
	if _, _, err := countEncodedOggPackets(path); err == nil {
		t.Fatal("countEncodedOggPackets accepted an invalid Ogg stream")
	}
}

func TestEncodeWithLibopusRejectsUnsupportedFrameBeforeCreatingOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid-frame.opus")
	_, err := encodeWithLibopus(path, 1, 64000, 1, 0, "sine")
	if err == nil || !strings.Contains(err.Error(), "unsupported external encoder frame size") {
		t.Fatalf("encodeWithLibopus error = %v, want unsupported frame size", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output stat error = %v, want output to remain absent", err)
	}
}

func TestRunRemovesTemporaryOutputAfterEncodeFailure(t *testing.T) {
	tempDir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP", "SystemTemp"} {
		t.Setenv(key, tempDir)
	}

	err := run([]string{"-play", "-duration=0"})
	if err == nil || !strings.Contains(err.Error(), "duration must be a positive finite number") {
		t.Fatalf("run error = %v, want invalid duration error", err)
	}

	leftovers, err := filepath.Glob(filepath.Join(os.TempDir(), "gopus_encode_*.opus"))
	if err != nil {
		t.Fatalf("Glob temporary output: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary Opus output remains after encode failure: %v", leftovers)
	}
}
