//go:build trace
// +build trace

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestTraceDecoderOutput(t *testing.T) {
	sampleRate := 48000
	frameSize := 960
	freq := 440.0

	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)
	packet, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	t.Logf("Encoded %d bytes", len(packet))

	dec := celt.NewDecoder(1)
	decoded, err := dec.DecodeFrame(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	t.Logf("Decoded %d samples", len(decoded))

	// Find max and its position
	maxVal := 0.0
	maxPos := 0
	for i, v := range decoded {
		if math.Abs(v) > maxVal {
			maxVal = math.Abs(v)
			maxPos = i
		}
	}
	t.Logf("Max |value| = %.4f at position %d", maxVal, maxPos)

	// Show samples around max
	t.Logf("\nSamples around max (pos %d):", maxPos)
	start := maxPos - 10
	if start < 0 {
		start = 0
	}
	end := maxPos + 10
	if end > len(decoded) {
		end = len(decoded)
	}
	for i := start; i < end; i++ {
		t.Logf("  [%4d] orig=%.5f decoded=%.5f", i, pcm[i], decoded[i])
	}

	// Compute mean absolute value
	var mean float64
	for _, v := range decoded {
		mean += math.Abs(v)
	}
	mean /= float64(len(decoded))
	t.Logf("\nMean |decoded|: %.5f", mean)

	// Show first and last 10 samples
	t.Log("\nFirst 10 samples:")
	for i := 0; i < 10; i++ {
		t.Logf("  [%4d] orig=%.5f decoded=%.5f", i, pcm[i], decoded[i])
	}
	t.Log("\nLast 10 samples:")
	for i := len(decoded) - 10; i < len(decoded); i++ {
		t.Logf("  [%4d] orig=%.5f decoded=%.5f", i, pcm[i], decoded[i])
	}

	// Also decode with libopus
	t.Log("\n=== Comparing with libopus decoder ===")

	// Add TOC for libopus
	toc := byte((31 << 3) | 0)
	libPacket := append([]byte{toc}, packet...)

	libDec, err := NewLibopusDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(libPacket, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	// Find max for libopus
	libMaxVal := float32(0)
	libMaxPos := 0
	for i := 0; i < libSamples; i++ {
		if v := float32(math.Abs(float64(libDecoded[i]))); v > libMaxVal {
			libMaxVal = v
			libMaxPos = i
		}
	}
	t.Logf("Libopus max |value| = %.4f at position %d", libMaxVal, libMaxPos)

	// Compare at same positions
	t.Log("\nComparison at max position:")
	for i := start; i < end && i < libSamples; i++ {
		t.Logf("  [%4d] gopus=%.5f libopus=%.5f diff=%.5f",
			i, decoded[i], libDecoded[i], decoded[i]-float64(libDecoded[i]))
	}

	// Compute SNR between gopus and libopus outputs
	var signalPower, noisePower float64
	count := minInt2(len(decoded), libSamples)
	for i := 0; i < count; i++ {
		signalPower += float64(libDecoded[i]) * float64(libDecoded[i])
		noise := decoded[i] - float64(libDecoded[i])
		noisePower += noise * noise
	}
	if noisePower > 0 {
		snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
		t.Logf("\nSNR (gopus vs libopus): %.2f dB", snr)
	}
}
