package main

import "testing"

// TestRoundtripInt16AllRates verifies a 20 ms int16 frame round-trips at every
// supported API sample rate and the decoder returns one full frame each time.
func TestRoundtripInt16AllRates(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		frame := rate / 50
		n, err := roundtripInt16(rate, frame)
		if err != nil {
			t.Fatalf("%d Hz: %v", rate, err)
		}
		if n != frame {
			t.Fatalf("%d Hz: decoded %d samples, want %d", rate, n, frame)
		}
	}
}
