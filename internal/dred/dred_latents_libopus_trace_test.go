//go:build gopus_dred || gopus_extra_controls

package dred

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusDREDLatentsTraceHelper libopustest.HelperCache

func getLibopusDREDLatentsTraceHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDREDLatentsTraceHelper, "libopus_dred_latents_trace.c", "gopus_libopus_dred_latents_trace")
}

type libopusDREDFrameTrace struct {
	FrameIdx    int
	LatentsFill int
	DREDOffset  int
	LatentOff   int
	Latents     [][rdovae.LatentDim]float32
}

func probeLibopusDREDLatentsTrace(t *testing.T, channels int) ([]libopusDREDFrameTrace, []float32) {
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
	var convert16k []float32
	offset := 0
	for offset < len(data) {
		if len(data)-offset < 4 {
			t.Fatalf("unexpected helper output at offset %d", offset)
		}
		switch string(data[offset : offset+4]) {
		case "GDLT":
			if len(data)-offset < 24 {
				t.Fatalf("truncated GDLT record at offset %d", offset)
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
		case "GDLC":
			if len(data)-offset < 8 {
				t.Fatalf("truncated GDLC record at offset %d", offset)
			}
			count := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
			pos := offset + 8
			if len(data)-pos < 4*count {
				t.Fatalf("truncated GDLC payload at offset %d", offset)
			}
			convert16k = make([]float32, count)
			for i := 0; i < count; i++ {
				convert16k[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[pos+4*i : pos+4*(i+1)]))
			}
			offset = pos + 4*count
		default:
			t.Fatalf("unexpected helper output magic %q at offset %d", string(data[offset:offset+4]), offset)
		}
	}
	return traces, convert16k
}

// dredLatentsTraceVoicedSample mirrors voiced_sample() in
// tools/csrc/libopus_dred_latents_trace.c so the Go side can rebuild the exact
// probe PCM the helper used for its 16 kHz conversion-sequence dump.
func dredLatentsTraceVoicedSample(frameIdx, sampleIdx, frameSize, sampleRate int) float32 {
	n := float64(frameIdx*frameSize + sampleIdx)
	t := n / float64(sampleRate)
	const twoPi = 2.0 * math.Pi
	env := 0.82 + 0.18*math.Sin(twoPi*1.3*t)
	s := 0.28 * math.Sin(twoPi*110.0*t)
	s += 0.17 * math.Sin(twoPi*220.0*t+0.11)
	s += 0.09 * math.Sin(twoPi*330.0*t+0.23)
	s += 0.05 * math.Sin(twoPi*440.0*t+0.37)
	return float32(env * s)
}

// gopusDREDConvertSequence replays the encoder-side dred_compute_latents() inner
// 16 kHz conversion loop the way encoder/dred_runtime.go convertDREDFrameTo16k()
// does: ConvertTo16kMonoFloat32 per process iteration plus the channel-blind
// `input = input[processSize:]` advance that mirrors libopus dred_encoder.c:240.
// It returns the ordered 16 kHz mono samples that would feed the RDOVAE encoder.
func gopusDREDConvertSequence(t *testing.T, channels int) []float32 {
	t.Helper()
	const (
		sampleRate = 48000
		frameSize  = 1920 // helper default chunk_size == frame_size
	)
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		v := dredLatentsTraceVoicedSample(0, i, frameSize, sampleRate)
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = v
		}
	}

	out := make([]float32, frameSize*16000/sampleRate)
	var mem [ResamplingOrder + 1]float32
	input := pcm
	produced := 0
	for remaining16k := len(out); remaining16k > 0; {
		processSize16k := DFrameSize
		if processSize16k > remaining16k {
			processSize16k = remaining16k
		}
		processSize := processSize16k * sampleRate / 16000
		processSamples := processSize * channels
		if processSamples <= 0 || processSamples > len(input) {
			t.Fatalf("gopus convert loop underflow: processSamples=%d len(input)=%d", processSamples, len(input))
		}
		n := ConvertTo16kMonoFloat32(out[produced:], &mem, input[:processSamples], sampleRate, channels)
		if n != processSize16k {
			t.Fatalf("ConvertTo16kMonoFloat32 produced %d want %d", n, processSize16k)
		}
		produced += n
		// Match libopus dred_compute_latents() pcm advancement: `pcm += process_size`
		// (dred_encoder.c:240) is channel-blind, so the interleaved-stereo pointer
		// advances by processSize floats rather than processSize*channels. The
		// downmix loop reads in[2*i]/in[2*i+1] (dred_convert_to_16k, lines 169-172),
		// so on multi-iter 40 ms / 60 ms calls stereo re-reads an overlapping window.
		input = input[processSize:]
		remaining16k -= processSize16k
	}
	return out[:produced]
}

// TestLibopusDREDLatentsTraceStereoMatchesOracle verifies that gopus's
// encoder-side stereo DRED latent pointer/buffer indexing reproduces the
// libopus oracle exactly. The only channel-dependent step in
// dred_compute_latents() is the 16 kHz conversion loop: the stereo downmix plus
// the channel-blind `pcm += process_size` advance (dred_encoder.c:219-242). The
// RDOVAE latent extraction itself runs on the resulting 16 kHz mono buffer and
// is channel-independent (dred_process_frame, dred_encoder.c:90-111), so byte
// parity of the conversion sequence pins latent parity for stereo. The libopus
// helper replays the same loop through the real static dred_convert_to_16k(),
// giving the oracle for both mono and stereo.
func TestLibopusDREDLatentsTraceStereoMatchesOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("channels=%d", channels), func(t *testing.T) {
			traces, want := probeLibopusDREDLatentsTrace(t, channels)
			if len(traces) < 2 {
				t.Fatalf("expected at least two latents-trace frames, got %d", len(traces))
			}
			if len(want) == 0 {
				t.Fatalf("libopus helper emitted no 16 kHz conversion sequence")
			}
			got := gopusDREDConvertSequence(t, channels)
			if len(got) != len(want) {
				t.Fatalf("16 kHz conversion length=%d want %d", len(got), len(want))
			}
			for i := range want {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("16 kHz conversion sample %d: gopus=%v (0x%08x) libopus=%v (0x%08x)",
						i, got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
				}
			}
		})
	}
}

// TestLibopusDREDLatentsTraceStereoDivergesFromMono documents the libopus
// stereo-vs-mono DRED latent divergence caused by the channel-blind
// `pcm += process_size` in dred_compute_latents() (dred_encoder.c:240). On
// multi-iter calls (40 ms / 60 ms at 48 kHz) stereo re-reads an overlapping PCM
// window while mono advances by the full chunk. gopus reproduces this exactly
// (verified against the oracle by TestLibopusDREDLatentsTraceStereoMatchesOracle),
// so this check pins the libopus behavior: DFrame 1 still matches between stereo
// and mono since the first iter shares the same starting pointer; DFrame 2
// onwards diverges because of the under-advanced pointer.
func TestLibopusDREDLatentsTraceStereoDivergesFromMono(t *testing.T) {
	libopustest.RequireOracle(t)
	monoTraces, _ := probeLibopusDREDLatentsTrace(t, 1)
	stereoTraces, _ := probeLibopusDREDLatentsTrace(t, 2)
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
