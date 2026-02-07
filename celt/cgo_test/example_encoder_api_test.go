//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
)

// TestExampleStyleEncoderWithLibopusDecode mirrors the public Encoder usage flow
// from examples and validates that libopus decodes packets with reasonable signal
// correlation over a short sequence.
func TestExampleStyleEncoderWithLibopusDecode(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		numFrames  = 6
		freqHz     = 440.0
		amp        = 0.5
	)

	enc, err := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	if err := enc.SetBitrate(64000); err != nil {
		t.Fatalf("SetBitrate failed: %v", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		t.Fatalf("SetComplexity failed: %v", err)
	}

	libDec, err := NewLibopusDecoder(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	totalSamples := frameSize * numFrames * channels
	orig := make([]float32, totalSamples)
	decoded := make([]float32, totalSamples)

	for f := 0; f < numFrames; f++ {
		frameStart := f * frameSize * channels
		frameEnd := frameStart + frameSize*channels
		frame := orig[frameStart:frameEnd]

		for i := 0; i < frameSize; i++ {
			ti := float64(f*frameSize+i) / float64(sampleRate)
			s := float32(amp * math.Sin(2*math.Pi*freqHz*ti))
			frame[2*i] = s
			frame[2*i+1] = s
		}

		packet, err := enc.EncodeFloat32(frame)
		if err != nil {
			t.Fatalf("EncodeFloat32 frame %d failed: %v", f, err)
		}
		if len(packet) == 0 {
			t.Fatalf("EncodeFloat32 frame %d produced empty packet", f)
		}

		out, n := libDec.DecodeFloat(packet, frameSize)
		if n <= 0 {
			t.Fatalf("libopus decode frame %d failed: n=%d", f, n)
		}
		copy(decoded[frameStart:frameStart+n], out[:n])
	}

	// Ignore edge samples and compute lagged correlation on a middle frame.
	// Opus introduces algorithmic delay, so we search a small lag window.
	mid := 2
	start := mid * frameSize * channels
	end := start + frameSize*channels
	trim := 120 * channels
	start += trim
	end -= trim
	if end <= start {
		t.Fatalf("invalid correlation window")
	}

	bestCorr := -1.0
	bestLag := 0
	const maxLag = 320 * channels
	for lag := -maxLag; lag <= maxLag; lag++ {
		var dot, xx, yy float64
		for i := start; i < end; i++ {
			j := i + lag
			if j < 0 || j >= len(decoded) {
				continue
			}
			x := float64(orig[i])
			y := float64(decoded[j])
			dot += x * y
			xx += x * x
			yy += y * y
		}
		if xx == 0 || yy == 0 {
			continue
		}
		c := dot / math.Sqrt(xx*yy)
		if c > bestCorr {
			bestCorr = c
			bestLag = lag
		}
	}
	t.Logf("example-style encoder best lagged correlation: %.4f (lag=%d samples)", bestCorr, bestLag)

	if bestCorr < 0.30 {
		t.Fatalf("low lagged correlation: got %.4f, want >= 0.30", bestCorr)
	}
}
