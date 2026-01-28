// Package cgo provides byte-level comparison tests between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestByteCompare_GopusDecodeGopusEncoded tests that gopus can decode what it encodes.
func TestByteCompare_GopusDecodeGopusEncoded(t *testing.T) {
	frameSize := 960
	channels := 1
	freq := 440.0

	// Generate test signal
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2.0*math.Pi*freq*float64(i)/48000.0)
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)
	encoded, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	t.Logf("Encoded %d bytes", len(encoded))
	t.Logf("First 32 bytes: %v", encoded[:minIntBC(32, len(encoded))])

	// Decode with gopus
	decoder := celt.NewDecoder(channels)
	decoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("Gopus decode failed: %v", err)
	}

	// Compute mean
	var gopusMean float64
	for _, s := range decoded {
		gopusMean += math.Abs(s)
	}
	gopusMean /= float64(len(decoded))
	t.Logf("Gopus decoded mean: %.6f (should be ~0.3)", gopusMean)

	if gopusMean < 0.01 {
		t.Errorf("Gopus self-decode produces silence (mean=%.6f)", gopusMean)
	}
}

// TestByteCompare_LibopusDecodeGopusEncoded tests that libopus can decode what gopus encodes.
func TestByteCompare_LibopusDecodeGopusEncoded(t *testing.T) {
	frameSize := 960
	channels := 1
	freq := 440.0

	// Generate test signal
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2.0*math.Pi*freq*float64(i)/48000.0)
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)
	encoded, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Add TOC byte for libopus
	// Config 31 = CELT FB 20ms, mono, code 0
	toc := byte((31 << 3) | 0)
	packet := append([]byte{toc}, encoded...)

	t.Logf("Packet for libopus: TOC=0x%02X, total %d bytes", toc, len(packet))
	t.Logf("Packet first 32 bytes: %v", packet[:minIntBC(32, len(packet))])

	// Decode with libopus
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	// Compute mean
	var libMean float64
	for i := 0; i < libSamples; i++ {
		libMean += math.Abs(float64(libDecoded[i]))
	}
	libMean /= float64(libSamples)
	t.Logf("Libopus decoded mean: %.10f (should be ~0.3)", libMean)

	if libMean < 0.001 {
		t.Errorf("SILENCE: libopus decode of gopus-encoded packet produces silence (mean=%.10f)", libMean)
	}
}

// TestByteCompare_FirstByteAnalysis analyzes the first byte after TOC to understand the encoding.
func TestByteCompare_FirstByteAnalysis(t *testing.T) {
	frameSize := 960
	channels := 1
	freq := 440.0

	// Generate test signal
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2.0*math.Pi*freq*float64(i)/48000.0)
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)
	encoded, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	t.Logf("Encoded %d bytes", len(encoded))

	// Analyze the first few bytes
	// Expected order: silence(logp=15), postfilter(logp=1), transient(logp=3, if LM>0), intra(logp=3), coarse energy
	t.Log("\nBitstream analysis:")
	t.Log("Expected encoding order:")
	t.Log("  1. Silence flag (logp=15) - very rare to be 1")
	t.Log("  2. Postfilter flag (logp=1)")
	t.Log("  3. Transient flag (logp=3) - for 20ms frames")
	t.Log("  4. Intra flag (logp=3)")
	t.Log("  5. Coarse energy (Laplace-coded values)")

	if len(encoded) > 0 {
		t.Logf("\nFirst byte: 0x%02X = %08b", encoded[0], encoded[0])
		// The range coder uses a different bit layout than direct bit packing
		// First byte contains the high bits of the range coded output
	}

	if len(encoded) > 1 {
		t.Logf("Second byte: 0x%02X = %08b", encoded[1], encoded[1])
	}

	// Also check what libopus produces for the same signal
	// This would require CGO encoding, which is more complex
	t.Log("\nTo properly compare, we need to encode with libopus and compare bytes.")
}

// TestByteCompare_SilenceFrame tests silence frame encoding.
func TestByteCompare_SilenceFrame(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate silence
	pcm := make([]float64, frameSize)

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)
	encoded, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	t.Logf("Silence frame encoded to %d bytes: %v", len(encoded), encoded)

	// For silence, the encoder should encode silence=1 which compresses to very few bytes
	// With logp=15, P(silence=1) = 1/32768, so the range coder expands it to multiple bytes
	// But our input is actually silence, so it should encode it.

	// Add TOC byte
	toc := byte((31 << 3) | 0)
	packet := append([]byte{toc}, encoded...)

	// Decode with libopus
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	// For silence, energy should be very low
	var libEnergy float64
	for i := 0; i < libSamples; i++ {
		libEnergy += float64(libDecoded[i]) * float64(libDecoded[i])
	}
	t.Logf("Libopus decoded silence energy: %.10f", libEnergy)
}

func minIntBC(a, b int) int {
	if a < b {
		return a
	}
	return b
}
