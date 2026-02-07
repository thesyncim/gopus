// Package testvectors provides gopus-to-gopus SILK roundtrip diagnostic tests.
// This test bypasses opusdec entirely to measure the actual encoder quality.
package testvectors

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestSILKRoundtripGopus encodes with gopus SILK encoder and decodes with gopus decoder.
// This bypasses opusdec to get the true encoder roundtrip quality.
func TestSILKRoundtripGopus(t *testing.T) {
	type testCase struct {
		name      string
		bandwidth types.Bandwidth
		frameSize int
		channels  int
		bitrate   int
	}

	cases := []testCase{
		{"SILK-NB-20ms-mono-16k", types.BandwidthNarrowband, 960, 1, 16000},
		{"SILK-WB-20ms-mono-32k", types.BandwidthWideband, 960, 1, 32000},
		{"SILK-NB-20ms-mono-32k", types.BandwidthNarrowband, 960, 1, 32000},
		{"SILK-WB-20ms-mono-48k", types.BandwidthWideband, 960, 1, 48000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testSILKRoundtripGopus(t, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
		})
	}
}

func testSILKRoundtripGopus(t *testing.T, bandwidth types.Bandwidth, frameSize, channels, bitrate int) {
	const sampleRate = 48000
	numFrames := 50 // 1 second of audio

	// Generate test signal
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	// Create encoder
	enc := encoder.NewEncoder(sampleRate, channels)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(bandwidth)
	enc.SetBitrate(bitrate)

	// Encode all frames
	packets := make([][]byte, numFrames)
	samplesPerFrame := frameSize * channels

	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", i, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Empty packet at frame %d", i)
		}
		// Copy packet since Encode returns a slice backed by scratch memory.
		packetCopy := make([]byte, len(packet))
		copy(packetCopy, packet)
		packets[i] = packetCopy
	}

	// Log packet sizes
	totalBytes := 0
	for _, p := range packets {
		totalBytes += len(p)
	}
	t.Logf("Encoded %d frames, total %d bytes, avg %.1f bytes/frame", numFrames, totalBytes, float64(totalBytes)/float64(numFrames))

	// Decode with gopus decoder
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder failed: %v", err)
	}

	decoded := make([]float32, 0, totalSamples)
	pcmBuf := make([]float32, frameSize*channels)

	for i, pkt := range packets {
		n, err := dec.Decode(pkt, pcmBuf)
		if err != nil {
			t.Fatalf("Decode frame %d failed: %v", i, err)
		}
		decoded = append(decoded, pcmBuf[:n*channels]...)
	}

	t.Logf("Decoded %d samples total", len(decoded))

	// Compute quality with delay compensation
	compareLen := len(original)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}

	q, foundDelay := ComputeQualityFloat32WithDelay(decoded[:compareLen], original[:compareLen], sampleRate, 960)
	snr := SNRFromQuality(q)
	t.Logf("gopus->gopus roundtrip: Q=%.2f, SNR=%.2f dB, delay=%d samples (%.1f ms)",
		q, snr, foundDelay, float64(foundDelay)/48.0)

	// Also compute raw SNR without delay search at delay=0
	var sigPow, noisePow float64
	for i := 0; i < compareLen; i++ {
		ref := float64(original[i])
		dec := float64(decoded[i])
		sigPow += ref * ref
		noise := dec - ref
		noisePow += noise * noise
	}
	rawSNR := 10 * math.Log10(sigPow/noisePow)
	t.Logf("Raw SNR (no delay comp): %.2f dB", rawSNR)

	// Check status
	if q >= EncoderStrictThreshold {
		t.Logf("STATUS: PASS (production quality)")
	} else if q >= EncoderGoodThreshold {
		t.Logf("STATUS: GOOD (acceptable)")
	} else if q >= EncoderQualityThreshold {
		t.Logf("STATUS: BASE (development baseline)")
	} else {
		t.Logf("STATUS: WARN (below baseline)")
	}

	// Show first 20 decoded vs original
	showN := 20
	if showN > compareLen {
		showN = compareLen
	}
	t.Logf("First %d samples:", showN)
	for i := 0; i < showN; i++ {
		t.Logf("  [%3d] orig=%.6f dec=%.6f diff=%.6f", i, original[i], decoded[i], decoded[i]-original[i])
	}

	// Show samples around the found delay offset
	if foundDelay > 0 && foundDelay < compareLen-20 {
		t.Logf("Samples around delay %d:", foundDelay)
		start := foundDelay - 3
		if start < 0 {
			start = 0
		}
		for i := start; i < start+20 && i < compareLen; i++ {
			decIdx := i
			origIdx := i - foundDelay
			if origIdx >= 0 && origIdx < len(original) {
				t.Logf("  dec[%d]=%.6f orig[%d]=%.6f", decIdx, decoded[decIdx], origIdx, original[origIdx])
			}
		}
	}
}
