package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/container/ogg"
)

func TestEncodeMixToOggPreservesAlignedAndPartialDuration(t *testing.T) {
	for _, totalSamples := range []int{2 * frameSize, 2*frameSize + 1} {
		t.Run(fmt.Sprintf("samples-%d", totalSamples), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mix.opus")
			pcm := make([]float32, totalSamples*channels)
			for i := 0; i < totalSamples; i++ {
				sample := float32(0.2 * math.Sin(float64(i)*2*math.Pi*440/sampleRate))
				pcm[i*channels] = sample
				pcm[i*channels+1] = sample
			}

			stats, err := encodeMixToOgg(path, pcm, 64000)
			if err != nil {
				t.Fatalf("encodeMixToOgg: %v", err)
			}
			if want := float64(totalSamples) / sampleRate; stats.durationSeconds != want {
				t.Fatalf("duration = %.9f, want %.9f", stats.durationSeconds, want)
			}

			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("open Ogg output: %v", err)
			}
			defer f.Close()

			reader, err := ogg.NewReader(f)
			if err != nil {
				t.Fatalf("create Ogg reader: %v", err)
			}
			preSkip := uint64(reader.PreSkip())
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
				t.Fatalf("audio packets = %d, stats report %d", packets, stats.frames)
			}
			if want := preSkip + uint64(totalSamples); finalGranule != want {
				t.Fatalf("final granule = %d, want pre-skip + input samples = %d", finalGranule, want)
			}
			if got := finalGranule - preSkip; got != uint64(totalSamples) {
				t.Fatalf("playable samples = %d, want %d", got, totalSamples)
			}
		})
	}
}
