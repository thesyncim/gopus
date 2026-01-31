// Package cgo provides CGO comparison tests for SILK decoding at native rate.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkNativeVsUpsampled compares timing by analyzing the signal shape
func TestSilkNativeVsUpsampled(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPackets(t, bitFile, 5)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	for i, pkt := range packets {
		if i > 2 {
			break
		}

		toc := gopus.ParseTOC(pkt[0])
		t.Logf("\nPacket %d: %d bytes, bandwidth=%d", i, len(pkt), toc.Bandwidth)

		// Decode with Go SILK at native rate
		silkDec := silk.NewDecoder()
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			t.Logf("  Invalid bandwidth for SILK")
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Get native sample rate
		config := silk.GetBandwidthConfig(silkBW)
		nativeRate := config.SampleRate
		t.Logf("  Native rate: %d Hz", nativeRate)

		// Initialize range decoder for SILK data (skip TOC byte)
		var rd rangecoding.Decoder
		if len(pkt) > 1 {
			rd.Init(pkt[1:])
		}

		// Decode at native rate
		nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("  gopus native decode failed: %v", err)
			continue
		}

		t.Logf("  Native samples: %d", len(nativeSamples))

		// Show first 30 native samples
		t.Log("  First 30 NATIVE samples (before resampling):")
		for j := 0; j < minInt(30, len(nativeSamples)); j++ {
			t.Logf("    [%2d] %.6f", j, nativeSamples[j])
		}

		// Now decode with full resampling and compare
		goDec, _ := gopus.NewDecoderDefault(48000, 1)
		goPcm, decErr := goDec.DecodeFloat32(pkt)
		if decErr != nil {
			t.Logf("  gopus full decode failed: %v", decErr)
			continue
		}

		// Also decode with libopus
		libDec, err := NewLibopusDecoder(48000, 1)
		if err != nil || libDec == nil {
			t.Fatalf("Failed to create libopus decoder")
		}
		defer libDec.Destroy()

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		if libSamples < 0 {
			t.Fatalf("libopus decode failed: %d", libSamples)
		}

		// Show samples around the transition (where values start changing)
		// The 6x upsample ratio means native[4] should appear around output[24-29]
		t.Logf("  Upsampled output comparison (looking for native[4]):")
		t.Logf("  native[4] = %.6f, native[5] = %.6f", nativeSamples[4], nativeSamples[5])

		// Show output samples 20-35
		t.Log("  Output samples 20-35:")
		t.Log("  Index\tgopus\t\tlibopus")
		for j := 20; j < minInt(36, minInt(len(goPcm), libSamples)); j++ {
			t.Logf("  %d\t%.6f\t%.6f", j, goPcm[j], libPcm[j])
		}
	}
}

// TestResamplerIsolated tests the resampler alone with known input
func TestResamplerIsolated(t *testing.T) {
	// Create a simple test signal: impulse at sample 4
	input := make([]float32, 20)
	input[4] = 1.0

	// Resample 8kHz -> 48kHz
	resampler := silk.NewLibopusResampler(8000, 48000)
	output := resampler.Process(input)

	t.Logf("Resampler test: 8kHz impulse at sample 4 -> 48kHz")
	t.Logf("Output length: %d (expected %d)", len(output), len(input)*6)

	// The impulse at input[4] should appear around output[24-29]
	t.Log("Output samples 20-35:")
	for i := 20; i < minInt(36, len(output)); i++ {
		marker := ""
		if output[i] != 0 {
			marker = " <- non-zero"
		}
		t.Logf("  [%2d] %.6f%s", i, output[i], marker)
	}

	// Find first non-zero sample
	firstNonZero := -1
	for i, v := range output {
		if v != 0 {
			firstNonZero = i
			break
		}
	}
	t.Logf("First non-zero sample at index: %d (expected around 24)", firstNonZero)
}

// loadPackets and minInt are defined in other test files in this package
