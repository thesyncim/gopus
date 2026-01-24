// Package testvectors provides round-trip testing for gopus encoder/decoder.
package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestGopusRoundTrip tests encoding and decoding with our own encoder/decoder.
func TestGopusRoundTrip(t *testing.T) {
	// Generate simple 440Hz sine wave
	pcm := make([]float64, 960*5) // 5 frames
	for i := range pcm {
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}

	// Create encoder and decoder
	enc := celt.NewEncoder(1) // mono
	dec := celt.NewDecoder(1)

	t.Log("Testing gopus encoder → gopus decoder round-trip")

	var encoded [][]byte
	var decoded []float64

	// Encode all frames
	for i := 0; i < 5; i++ {
		start := i * 960
		end := start + 960
		framePCM := pcm[start:end]

		packet, err := enc.EncodeFrame(framePCM, 960)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", i, err)
		}
		encoded = append(encoded, packet)
		t.Logf("Frame %d: encoded %d bytes", i, len(packet))
	}

	// Decode all frames
	for i, packet := range encoded {
		output, err := dec.DecodeFrame(packet, 960)
		if err != nil {
			t.Logf("Decode frame %d failed: %v", i, err)
			// Continue anyway to see what we can decode
		}
		decoded = append(decoded, output...)
		t.Logf("Frame %d: decoded %d samples", i, len(output))
	}

	// Compare input and output
	if len(decoded) == 0 {
		t.Fatal("No samples decoded")
	}

	compareLen := len(pcm)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}

	// Calculate SNR
	var signalPower, errorPower float64
	for i := 0; i < compareLen; i++ {
		signal := pcm[i]
		error := decoded[i] - signal
		signalPower += signal * signal
		errorPower += error * error
	}

	if errorPower > 0 {
		snr := 10 * math.Log10(signalPower/errorPower)
		t.Logf("SNR: %.2f dB", snr)
	} else {
		t.Log("SNR: Infinity (perfect reconstruction)")
	}

	// Check correlation
	var sumXY, sumX2, sumY2 float64
	for i := 0; i < compareLen; i++ {
		x := pcm[i]
		y := decoded[i]
		sumXY += x * y
		sumX2 += x * x
		sumY2 += y * y
	}
	corr := sumXY / (math.Sqrt(sumX2) * math.Sqrt(sumY2))
	t.Logf("Correlation: %.4f", corr)

	// Show first few samples
	t.Log("First 10 samples comparison:")
	for i := 0; i < 10 && i < compareLen; i++ {
		t.Logf("  %d: input=%.4f, decoded=%.4f, error=%.4f",
			i, pcm[i], decoded[i], decoded[i]-pcm[i])
	}

	// Check if decoded audio is silence
	maxDecoded := 0.0
	for _, v := range decoded {
		if math.Abs(v) > maxDecoded {
			maxDecoded = math.Abs(v)
		}
	}
	t.Logf("Max decoded amplitude: %.6f", maxDecoded)

	if maxDecoded < 0.001 {
		t.Error("Decoded audio appears to be silence!")
	}
}

// TestSimpleDecodeEncode tests a minimal encode-decode cycle.
func TestSimpleDecodeEncode(t *testing.T) {
	// Generate DC signal (simplest possible)
	pcm := make([]float64, 960)
	for i := range pcm {
		pcm[i] = 0.1 // Small constant
	}

	enc := celt.NewEncoder(1)
	dec := celt.NewDecoder(1)

	// Encode
	packet, err := enc.EncodeFrame(pcm, 960)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	t.Logf("Encoded %d bytes: %x", len(packet), packet[:min(20, len(packet))])

	// Decode
	decoded, err := dec.DecodeFrame(packet, 960)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	t.Logf("Decoded %d samples", len(decoded))

	// Check decoded values
	maxDecoded := 0.0
	for _, v := range decoded {
		if math.Abs(v) > maxDecoded {
			maxDecoded = math.Abs(v)
		}
	}
	t.Logf("Max decoded: %.6f", maxDecoded)
	t.Logf("First 10 decoded: %v", decoded[:min(10, len(decoded))])
}
