package main

import "testing"

// TestRoundtripSurround exercises the 5.1 multistream round trip the same way
// `go run .` does, so the example stays runnable.
func TestRoundtripSurround(t *testing.T) {
	const frames = 4

	decoded, layout, err := roundtripSurround(channels, frames)
	if err != nil {
		t.Fatalf("roundtripSurround: %v", err)
	}
	if layout == "" {
		t.Fatal("empty stream layout")
	}

	// 5.1 must decode to interleaved 6-channel PCM, one full frame per input.
	if got, want := len(decoded), frames*frameSize*channels; got != want {
		t.Fatalf("decoded samples = %d, want %d", got, want)
	}
}
