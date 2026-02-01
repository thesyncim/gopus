//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

func TestMultiFrameDecoder(t *testing.T) {
	sampleRate := 48000
	frameSize := 960
	freq := 440.0
	numFrames := 5

	// Generate multi-frame test signal
	totalSamples := frameSize * numFrames
	original := make([]float64, totalSamples)
	for i := 0; i < totalSamples; i++ {
		ti := float64(i) / float64(sampleRate)
		original[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	// Create encoder and decoder
	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)
	dec := celt.NewDecoder(1)

	// Encode all frames
	packets := make([][]byte, numFrames)
	for f := 0; f < numFrames; f++ {
		start := f * frameSize
		end := start + frameSize
		pcm := original[start:end]

		packet, err := enc.EncodeFrame(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", f, err)
		}
		packets[f] = packet
	}

	// Decode all frames
	decoded := make([]float64, totalSamples)
	for f := 0; f < numFrames; f++ {
		start := f * frameSize

		out, err := dec.DecodeFrame(packets[f], frameSize)
		if err != nil {
			t.Fatalf("Decode frame %d failed: %v", f, err)
		}
		copy(decoded[start:start+frameSize], out)
	}

	// Compare middle frame (frame 2, which should have proper overlap from both sides)
	middleFrame := 2
	frameStart := middleFrame * frameSize
	frameEnd := frameStart + frameSize

	t.Logf("Comparing frame %d (samples %d-%d)", middleFrame, frameStart, frameEnd)

	// Compute SNR for middle frame with delay compensation
	delay := celt.Overlap
	var signalPower, noisePower float64
	count := 0
	for i := frameStart + delay; i < frameEnd-delay && i+delay < totalSamples; i++ {
		origSample := original[i]
		decSample := decoded[i]

		signalPower += origSample * origSample
		noise := origSample - decSample
		noisePower += noise * noise
		count++
	}

	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("Middle frame SNR: %.2f dB (using %d samples, delay=%d)", snr, count, delay)

	// Show some samples from middle frame
	t.Log("\nMiddle frame samples (around center):")
	center := frameStart + frameSize/2
	for i := center - 5; i <= center+5; i++ {
		t.Logf("  [%d] orig=%.5f decoded=%.5f diff=%.5f",
			i, original[i], decoded[i], original[i]-decoded[i])
	}

	// Show max amplitude comparison
	var maxOrig, maxDec float64
	for i := frameStart; i < frameEnd; i++ {
		if math.Abs(original[i]) > maxOrig {
			maxOrig = math.Abs(original[i])
		}
		if math.Abs(decoded[i]) > maxDec {
			maxDec = math.Abs(decoded[i])
		}
	}
	t.Logf("\nMax amplitudes in frame %d: orig=%.4f decoded=%.4f", middleFrame, maxOrig, maxDec)

	// Also test with libopus decoding
	t.Log("\n=== Comparing with libopus multi-frame decode ===")

	libDec, err := NewLibopusDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded := make([]float64, totalSamples)
	for f := 0; f < numFrames; f++ {
		start := f * frameSize

		// Add TOC for libopus
		toc := byte((31 << 3) | 0)
		libPacket := append([]byte{toc}, packets[f]...)

		out, samples := libDec.DecodeFloat(libPacket, frameSize)
		if samples <= 0 {
			t.Fatalf("libopus decode frame %d failed: %d", f, samples)
		}
		for i := 0; i < samples && start+i < totalSamples; i++ {
			libDecoded[start+i] = float64(out[i])
		}
	}

	// Compare gopus vs libopus for middle frame
	var signalPowerLib, noisePowerLib float64
	for i := frameStart + delay; i < frameEnd-delay; i++ {
		origSample := original[i]
		libSample := libDecoded[i]

		signalPowerLib += origSample * origSample
		noise := origSample - libSample
		noisePowerLib += noise * noise
	}

	snrLib := 10 * math.Log10(signalPowerLib/(noisePowerLib+1e-10))
	t.Logf("Libopus middle frame SNR: %.2f dB", snrLib)

	// Compare gopus and libopus outputs
	var gpSignal, gpNoise float64
	for i := frameStart; i < frameEnd; i++ {
		gpSignal += libDecoded[i] * libDecoded[i]
		diff := decoded[i] - libDecoded[i]
		gpNoise += diff * diff
	}
	snrGpLib := 10 * math.Log10(gpSignal/(gpNoise+1e-10))
	t.Logf("Gopus vs Libopus decoder SNR: %.2f dB (should be high if both decode same)", snrGpLib)

	t.Log("\nLibopus middle frame samples (around center):")
	for i := center - 5; i <= center+5; i++ {
		t.Logf("  [%d] orig=%.5f libdec=%.5f diff=%.5f",
			i, original[i], libDecoded[i], original[i]-libDecoded[i])
	}
}
