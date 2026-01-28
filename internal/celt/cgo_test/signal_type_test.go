// Package cgo tests different signal types for encoding compatibility.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestSignalType_DC tests encoding and decoding of a DC (constant) signal.
func TestSignalType_DC(t *testing.T) {
	frameSize := 960
	channels := 1
	dcValue := 0.3

	// Generate DC signal
	pcm := make([]float64, frameSize)
	for i := range pcm {
		pcm[i] = dcValue
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)
	encoded, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	t.Logf("DC signal encoded to %d bytes", len(encoded))
	t.Logf("First 32 bytes: %v", encoded[:minIntST(32, len(encoded))])

	// Decode with gopus
	decoder := celt.NewDecoder(channels)
	gopusDecoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("Gopus decode failed: %v", err)
	}

	var gopusMean float64
	for _, s := range gopusDecoded {
		gopusMean += s
	}
	gopusMean /= float64(len(gopusDecoded))
	t.Logf("Gopus decoded DC: mean=%.6f (expected %.2f)", gopusMean, dcValue)

	// Decode with libopus
	toc := byte((31 << 3) | 0) // CELT FB 20ms mono
	packet := append([]byte{toc}, encoded...)

	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	var libMean float64
	for i := 0; i < libSamples; i++ {
		libMean += float64(libDecoded[i])
	}
	libMean /= float64(libSamples)
	t.Logf("Libopus decoded DC: mean=%.10f (expected %.2f)", libMean, dcValue)

	// Compare
	if math.Abs(gopusMean-dcValue) < 0.1 && math.Abs(libMean) < 0.01 {
		t.Errorf("ISSUE: DC signal decodes correctly in gopus (%.6f) but not in libopus (%.10f)", gopusMean, libMean)
	}
}

// TestSignalType_Sine tests encoding and decoding of a sine wave.
func TestSignalType_Sine(t *testing.T) {
	frameSize := 960
	channels := 1
	freq := 440.0
	amplitude := 0.5

	// Generate sine wave
	pcm := make([]float64, frameSize)
	for i := range pcm {
		pcm[i] = amplitude * math.Sin(2.0*math.Pi*freq*float64(i)/48000.0)
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)
	encoded, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	t.Logf("Sine wave encoded to %d bytes", len(encoded))
	t.Logf("First 32 bytes: %v", encoded[:minIntST(32, len(encoded))])

	// Decode with gopus
	decoder := celt.NewDecoder(channels)
	gopusDecoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("Gopus decode failed: %v", err)
	}

	var gopusMean float64
	for _, s := range gopusDecoded {
		gopusMean += math.Abs(s)
	}
	gopusMean /= float64(len(gopusDecoded))
	t.Logf("Gopus decoded sine: mean absolute=%.6f (expected ~%.2f)", gopusMean, amplitude*2/math.Pi)

	// Decode with libopus
	toc := byte((31 << 3) | 0)
	packet := append([]byte{toc}, encoded...)

	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	var libMean float64
	for i := 0; i < libSamples; i++ {
		libMean += math.Abs(float64(libDecoded[i]))
	}
	libMean /= float64(libSamples)
	t.Logf("Libopus decoded sine: mean absolute=%.10f (expected ~%.2f)", libMean, amplitude*2/math.Pi)

	if libMean < 0.01 {
		t.Errorf("ISSUE: Sine wave produces silence when decoded by libopus")
	}
}

// TestSignalType_MultipleFrames tests encoding and decoding across multiple frames.
func TestSignalType_MultipleFrames(t *testing.T) {
	frameSize := 960
	channels := 1
	freq := 440.0
	amplitude := 0.5
	numFrames := 5

	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)

	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	for frame := 0; frame < numFrames; frame++ {
		// Generate sine wave for this frame
		pcm := make([]float64, frameSize)
		startSample := frame * frameSize
		for i := range pcm {
			pcm[i] = amplitude * math.Sin(2.0*math.Pi*freq*float64(startSample+i)/48000.0)
		}

		// Encode
		encoded, err := encoder.EncodeFrame(pcm, frameSize)
		if err != nil {
			t.Fatalf("Frame %d encode failed: %v", frame, err)
		}

		// Decode with libopus
		toc := byte((31 << 3) | 0)
		packet := append([]byte{toc}, encoded...)

		libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
		if libSamples <= 0 {
			t.Fatalf("Frame %d libopus decode failed: %d", frame, libSamples)
		}

		var libMean float64
		for i := 0; i < libSamples; i++ {
			libMean += math.Abs(float64(libDecoded[i]))
		}
		libMean /= float64(libSamples)
		t.Logf("Frame %d: libopus decoded mean absolute = %.6f", frame, libMean)

		if libMean < 0.01 {
			t.Errorf("Frame %d: Sine wave produces silence when decoded by libopus", frame)
		}
	}
}

// TestSignalType_DCvsSine compares DC and sine encoding directly.
func TestSignalType_DCvsSine(t *testing.T) {
	frameSize := 960
	channels := 1

	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)

	// Test 1: DC signal
	t.Log("=== DC Signal ===")
	dcPcm := make([]float64, frameSize)
	for i := range dcPcm {
		dcPcm[i] = 0.3
	}
	dcEncoded, err := encoder.EncodeFrame(dcPcm, frameSize)
	if err != nil {
		t.Fatalf("DC encode failed: %v", err)
	}
	t.Logf("DC encoded: %d bytes, first 16: %v", len(dcEncoded), dcEncoded[:minIntST(16, len(dcEncoded))])

	// Reset encoder for sine test
	encoder.Reset()

	// Test 2: Sine signal
	t.Log("=== Sine Signal ===")
	sinePcm := make([]float64, frameSize)
	for i := range sinePcm {
		sinePcm[i] = 0.3 * math.Sin(2.0*math.Pi*440.0*float64(i)/48000.0)
	}
	sineEncoded, err := encoder.EncodeFrame(sinePcm, frameSize)
	if err != nil {
		t.Fatalf("Sine encode failed: %v", err)
	}
	t.Logf("Sine encoded: %d bytes, first 16: %v", len(sineEncoded), sineEncoded[:minIntST(16, len(sineEncoded))])

	// Compare first byte (should be different due to different energy distribution)
	t.Logf("First byte comparison: DC=0x%02X, Sine=0x%02X", dcEncoded[0], sineEncoded[0])
}

func minIntST(a, b int) int {
	if a < b {
		return a
	}
	return b
}
