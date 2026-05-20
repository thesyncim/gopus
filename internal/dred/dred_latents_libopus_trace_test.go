//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package dred

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var (
	libopusDREDLatentsTraceHelperOnce sync.Once
	libopusDREDLatentsTraceHelperPath string
	libopusDREDLatentsTraceHelperErr  error
)

func getLibopusDREDLatentsTraceHelperPath() (string, error) {
	libopusDREDLatentsTraceHelperOnce.Do(func() {
		libopusDREDLatentsTraceHelperPath, libopusDREDLatentsTraceHelperErr = buildLibopusDREDHelper("libopus_dred_latents_trace.c", "gopus_libopus_dred_latents_trace")
	})
	if libopusDREDLatentsTraceHelperErr != nil {
		return "", libopusDREDLatentsTraceHelperErr
	}
	return libopusDREDLatentsTraceHelperPath, nil
}

type libopusDREDFrameTrace struct {
	FrameIdx    int
	LatentsFill int
	DREDOffset  int
	LatentOff   int
	Latents     [][rdovae.LatentDim]float32
}

func probeLibopusDREDLatentsTrace(t *testing.T, channels int) []libopusDREDFrameTrace {
	t.Helper()
	binPath, err := getLibopusDREDLatentsTraceHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred latents trace", err)
	}
	data, err := libopustest.RunHelperArgs(binPath, nil, fmt.Sprintf("%d", channels))
	if err != nil {
		t.Fatalf("run libopus dred latents trace helper: %v", err)
	}
	var traces []libopusDREDFrameTrace
	offset := 0
	for offset < len(data) {
		if len(data)-offset < 24 || string(data[offset:offset+4]) != "GDLT" {
			t.Fatalf("unexpected helper output at offset %d", offset)
		}
		var trace libopusDREDFrameTrace
		trace.FrameIdx = int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		trace.LatentsFill = int(binary.LittleEndian.Uint32(data[offset+8 : offset+12]))
		trace.DREDOffset = int(binary.LittleEndian.Uint32(data[offset+12 : offset+16]))
		trace.LatentOff = int(binary.LittleEndian.Uint32(data[offset+16 : offset+20]))
		positionCount := int(binary.LittleEndian.Uint32(data[offset+20 : offset+24]))
		pos := offset + 24
		trace.Latents = make([][rdovae.LatentDim]float32, positionCount)
		for i := 0; i < positionCount; i++ {
			var row [rdovae.LatentDim]float32
			for j := 0; j < rdovae.LatentDim; j++ {
				row[j] = math.Float32frombits(binary.LittleEndian.Uint32(data[pos+4*j : pos+4*(j+1)]))
			}
			pos += 4 * rdovae.LatentDim
			trace.Latents[i] = row
		}
		traces = append(traces, trace)
		offset = pos
	}
	return traces
}

// TestLibopusDREDLatentsTraceStereoDivergesFromMono confirms the libopus
// stereo-vs-mono DRED latent divergence introduced by `pcm += process_size` in
// dred_compute_latents() (dred_encoder.c:240). The increment is missing a
// channels multiplier, so on multi-iter Process16k calls (40 ms / 60 ms at
// 48 kHz) stereo reads an overlapping PCM window while mono advances by the
// full chunk. This pins that libopus quirk so any future libopus refresh that
// fixes it will surface here and let us re-evaluate the gopus mirror in
// encoder/dred_runtime.go convertDREDFrameTo16k(). DFrame 1 still matches
// between stereo and mono since the first iter shares the same starting
// pointer; DFrame 2 onwards diverges because of the under-advanced pointer.
func TestLibopusDREDLatentsTraceStereoDivergesFromMono(t *testing.T) {
	libopustest.RequireOracle(t)
	monoTraces := probeLibopusDREDLatentsTrace(t, 1)
	stereoTraces := probeLibopusDREDLatentsTrace(t, 2)
	if len(monoTraces) < 2 || len(stereoTraces) < 2 {
		t.Fatalf("expected at least two frames per channel, got mono=%d stereo=%d", len(monoTraces), len(stereoTraces))
	}
	// Frame 0 DFrame 1 (oldest latent, position 1 of fill=2) should match
	// between mono and stereo: it predates the buggy pcm advance.
	for i, monoRow := range monoTraces[0].Latents {
		if i != 1 {
			continue
		}
		stereoRow := stereoTraces[0].Latents[i]
		for k := 0; k < rdovae.LatentDim; k++ {
			if math.Abs(float64(monoRow[k]-stereoRow[k])) > 5e-3 {
				t.Errorf("frame 0 DFrame 1 position %d k=%d: mono=%v stereo=%v expected match", i, k, monoRow[k], stereoRow[k])
			}
		}
	}
	// Frame 0 DFrame 2 (newest latent, position 0) MUST diverge between
	// mono and stereo to confirm the libopus advance bug is present.
	monoD2 := monoTraces[0].Latents[0]
	stereoD2 := stereoTraces[0].Latents[0]
	divergent := false
	for k := 0; k < rdovae.LatentDim; k++ {
		if math.Abs(float64(monoD2[k]-stereoD2[k])) > 1e-2 {
			divergent = true
			break
		}
	}
	if !divergent {
		t.Fatalf("expected libopus mono and stereo DFrame 2 latents to diverge; got identical values: mono=%v stereo=%v", monoD2, stereoD2)
	}
}
