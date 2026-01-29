package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestEncoderCompareWithLibopus encodes with both gopus and libopus,
// then decodes with libopus to compare quality
func TestEncoderCompareWithLibopus(t *testing.T) {
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

	// Convert to float32 for libopus
	original32 := make([]float32, totalSamples)
	for i, v := range original {
		original32[i] = float32(v)
	}

	// === Encode with gopus ===
	gopusEnc := celt.NewEncoder(1)
	gopusEnc.SetBitrate(64000)

	gopusPackets := make([][]byte, numFrames)
	for f := 0; f < numFrames; f++ {
		start := f * frameSize
		end := start + frameSize
		pcm := original[start:end]

		packet, err := gopusEnc.EncodeFrame(pcm, frameSize)
		if err != nil {
			t.Fatalf("gopus encode frame %d failed: %v", f, err)
		}
		gopusPackets[f] = packet
	}

	// === Encode with libopus ===
	libEnc, err := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)
	libEnc.SetSignal(OpusSignalMusic)

	libopusPackets := make([][]byte, numFrames)
	for f := 0; f < numFrames; f++ {
		start := f * frameSize
		end := start + frameSize
		pcm := original32[start:end]

		packet, n := libEnc.EncodeFloat(pcm, frameSize)
		if n < 0 {
			t.Fatalf("libopus encode frame %d failed: %d", f, n)
		}
		libopusPackets[f] = packet // includes TOC
	}

	// === Decode both with libopus ===
	libDec, err := NewLibopusDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	// Decode gopus-encoded packets
	t.Log("=== Decoding gopus-encoded packets with libopus ===")
	gopusDecoded := make([]float64, totalSamples)
	for f := 0; f < numFrames; f++ {
		start := f * frameSize
		// Add TOC for libopus
		toc := byte((31 << 3) | 0)
		packet := append([]byte{toc}, gopusPackets[f]...)

		out, samples := libDec.DecodeFloat(packet, frameSize)
		if samples <= 0 {
			t.Fatalf("libopus decode gopus frame %d failed: %d", f, samples)
		}
		for i := 0; i < samples && start+i < totalSamples; i++ {
			gopusDecoded[start+i] = float64(out[i])
		}
	}

	// Create new decoder for libopus-encoded packets (reset state)
	libDec2, err := NewLibopusDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewLibopusDecoder2 failed: %v", err)
	}
	defer libDec2.Destroy()

	t.Log("=== Decoding libopus-encoded packets with libopus ===")
	libopusDecoded := make([]float64, totalSamples)
	for f := 0; f < numFrames; f++ {
		start := f * frameSize
		// libopus packets already include TOC
		out, samples := libDec2.DecodeFloat(libopusPackets[f], frameSize)
		if samples <= 0 {
			t.Fatalf("libopus decode libopus frame %d failed: %d", f, samples)
		}
		for i := 0; i < samples && start+i < totalSamples; i++ {
			libopusDecoded[start+i] = float64(out[i])
		}
	}

	// === Compare quality ===
	middleFrame := 2
	frameStart := middleFrame * frameSize
	frameEnd := frameStart + frameSize
	delay := celt.Overlap

	// SNR for gopus-encoded
	var signalPowerGopus, noisePowerGopus float64
	for i := frameStart + delay; i < frameEnd-delay; i++ {
		signalPowerGopus += original[i] * original[i]
		noise := original[i] - gopusDecoded[i]
		noisePowerGopus += noise * noise
	}
	snrGopus := 10 * math.Log10(signalPowerGopus/(noisePowerGopus+1e-10))

	// SNR for libopus-encoded
	var signalPowerLib, noisePowerLib float64
	for i := frameStart + delay; i < frameEnd-delay; i++ {
		signalPowerLib += original[i] * original[i]
		noise := original[i] - libopusDecoded[i]
		noisePowerLib += noise * noise
	}
	snrLibopus := 10 * math.Log10(signalPowerLib/(noisePowerLib+1e-10))

	t.Logf("Middle frame (frame %d) SNR:", middleFrame)
	t.Logf("  gopus-encoded -> libopus-decoded: %.2f dB", snrGopus)
	t.Logf("  libopus-encoded -> libopus-decoded: %.2f dB", snrLibopus)

	// Show samples comparison
	center := frameStart + frameSize/2
	t.Log("\nSample comparison at center of middle frame:")
	t.Logf("  idx      original  gopus-enc  libopus-enc")
	for i := center - 5; i <= center+5; i++ {
		t.Logf("  [%d] %10.5f %10.5f %10.5f",
			i, original[i], gopusDecoded[i], libopusDecoded[i])
	}

	// Packet size comparison
	t.Log("\nPacket sizes:")
	for f := 0; f < numFrames; f++ {
		t.Logf("  Frame %d: gopus=%d bytes, libopus=%d bytes (includes TOC)",
			f, len(gopusPackets[f]), len(libopusPackets[f]))
	}

	// First bytes comparison for frame 0
	t.Log("\nFrame 0 first 20 bytes comparison:")
	t.Logf("  gopus:   %02x", gopusPackets[0][:minInt2(20, len(gopusPackets[0]))])
	libPayload := libopusPackets[0][1:] // skip TOC
	t.Logf("  libopus: %02x", libPayload[:minInt2(20, len(libPayload))])
}
