package encoder

import "testing"

func runDelayCompStream(frameSize, channels, totalFrames int) ([]float64, []float64) {
	e := NewEncoder(48000, channels)
	// Disable dcReject so test exercises only delay compensation behavior.
	e.sampleRate = 48000
	delaySamples := (e.sampleRate / 250) * channels
	frameSamples := frameSize * channels
	totalSamples := totalFrames * frameSamples

	in := make([]float64, totalSamples)
	for i := range in {
		in[i] = float64(i + 1)
	}

	out := make([]float64, 0, totalSamples)
	for f := 0; f < totalFrames; f++ {
		start := f * frameSamples
		end := start + frameSamples
		block := e.applyDelayCompensation(in[start:end], frameSize)
		out = append(out, block...)
	}

	want := make([]float64, totalSamples)
	for i := range want {
		src := i - delaySamples
		if src >= 0 {
			want[i] = in[src]
		}
	}
	return out, want
}

func TestDelayCompensation_StreamDelayMono(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480, 960} {
		out, want := runDelayCompStream(frameSize, 1, 24)
		if len(out) != len(want) {
			t.Fatalf("frame=%d: output len=%d want=%d", frameSize, len(out), len(want))
		}
		for i := range out {
			if out[i] != want[i] {
				t.Fatalf("frame=%d sample=%d: got=%.0f want=%.0f", frameSize, i, out[i], want[i])
			}
		}
	}
}

func TestDelayCompensation_StreamDelayStereo(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480, 960} {
		out, want := runDelayCompStream(frameSize, 2, 24)
		if len(out) != len(want) {
			t.Fatalf("frame=%d: output len=%d want=%d", frameSize, len(out), len(want))
		}
		for i := range out {
			if out[i] != want[i] {
				t.Fatalf("frame=%d sample=%d: got=%.0f want=%.0f", frameSize, i, out[i], want[i])
			}
		}
	}
}
