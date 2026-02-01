package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestOwnEncoderDecoder(t *testing.T) {
	// Generate 1 frame of simple sine wave
	sampleRate := 48000
	frameSize := 960
	freq := 440.0

	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	// Create CELT encoder
	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)

	// Encode
	packet, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	t.Logf("Encoded packet: %d bytes", len(packet))
	t.Logf("First 10 bytes: %x", packet[:10])

	// Create CELT decoder
	dec := celt.NewDecoder(1)

	// Decode with our own decoder
	decoded, err := dec.DecodeFrame(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	t.Logf("Decoded samples: %d", len(decoded))

	// Compare first 20 samples
	t.Log("\nFirst 20 samples:")
	t.Log("  i      original     decoded")
	for i := 0; i < 20 && i < len(pcm) && i < len(decoded); i++ {
		t.Logf("%3d  %10.5f  %10.5f", i, pcm[i], decoded[i])
	}

	// Compute metrics
	maxOrig, maxDec := 0.0, 0.0
	for _, v := range pcm {
		if math.Abs(v) > maxOrig {
			maxOrig = math.Abs(v)
		}
	}
	for _, v := range decoded {
		if math.Abs(v) > maxDec {
			maxDec = math.Abs(v)
		}
	}
	t.Logf("\nMax amplitudes: orig=%.4f, decoded=%.4f", maxOrig, maxDec)

	// Compute SNR (align for CELT algorithmic delay).
	// CELT adds the overlap (MDCT) plus encoder lookahead (delay compensation).
	delay := celt.Overlap + celt.DelayCompensation
	if delay >= len(decoded) {
		t.Fatalf("Decoded output too short for overlap delay: %d >= %d", delay, len(decoded))
	}
	if delay >= len(pcm) {
		t.Fatalf("Input too short for overlap delay: %d >= %d", delay, len(pcm))
	}

	n := len(pcm)
	if len(decoded)-delay < n {
		n = len(decoded) - delay
	}

	var signalPower, noisePower float64
	for i := 0; i < n; i++ {
		signalPower += pcm[i] * pcm[i]
		noise := pcm[i] - decoded[i+delay]
		noisePower += noise * noise
	}

	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))

	t.Logf("SNR: %.2f dB", snr)

	if snr < 5 {
		t.Errorf("SNR too low: %.2f dB", snr)
	}
}
