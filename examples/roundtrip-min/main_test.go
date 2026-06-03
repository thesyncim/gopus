package main

import (
	"math"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	pcm := make([]float32, frameSize*channels)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
	}

	packet, n, err := encodeDecode(pcm)
	if err != nil {
		t.Fatalf("encodeDecode: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("encodeDecode returned an empty packet")
	}
	if n != frameSize {
		t.Fatalf("decoded samples per channel = %d, want %d", n, frameSize)
	}
}
