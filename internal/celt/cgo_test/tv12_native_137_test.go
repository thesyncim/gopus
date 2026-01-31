// Package cgo compares native SILK output at packet 137 (NB→MB transition).
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Native137 compares native SILK output (before resampling) at packet 137.
func TestTV12Native137(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus SILK decoder and process packets 0-136
	silkDec := silk.NewDecoder()

	// Create libopus decoder at 48kHz and process packets 0-136
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Processing packets 0-136 to build state ===")

	// Process packets 0-136 with both decoders
	for i := 0; i < 137; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// libopus at 48kHz
		libDec.DecodeFloat(pkt, 1920)

		// gopus SILK at native rate
		if toc.Mode == gopus.ModeSILK {
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if ok {
				duration := silk.FrameDurationFromTOC(toc.FrameSize)
				var rd rangecoding.Decoder
				rd.Init(pkt[1:])
				silkDec.DecodeFrame(&rd, silkBW, duration, true)
			}
		}
	}

	t.Log("=== Comparing native output at packet 137 (first MB) ===")

	// Now decode packet 137 at native rate
	pkt := packets[137]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 137: Mode=%v BW=%d", toc.Mode, toc.Bandwidth)

	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	config := silk.GetBandwidthConfig(silkBW)
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	t.Logf("Native rate: %d Hz (%v), frameLength=%d", config.SampleRate, silkBW, config.SampleRate*20/1000)

	// Gopus: native SILK decode
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("Gopus decode error: %v", err)
	}

	// Get sMid state
	sMid := silkDec.GetSMid()
	t.Logf("sMid after decode: [%d, %d]", sMid[0], sMid[1])

	// Libopus: decode at 48kHz (we can't easily get native rate from libopus via this wrapper)
	libOut, libN := libDec.DecodeFloat(pkt, 1920)
	t.Logf("Gopus native samples: %d (at %d Hz)", len(goNative), config.SampleRate)
	t.Logf("Libopus 48kHz samples: %d", libN)

	// Show first 20 native samples from gopus
	t.Log("\nGopus native samples (first 20):")
	for i := 0; i < 20 && i < len(goNative); i++ {
		t.Logf("  [%2d] %+.6f (int16: %6d)", i, goNative[i], int16(goNative[i]*32768))
	}

	// Now let's resample gopus native to 48kHz and compare
	t.Log("\n=== Resampling gopus native to 48kHz ===")

	// Build resampler input (sMid[1] + native samples)
	_ = silkDec.GetSMid() // Should be from the PREVIOUS decode, not this one
	// Actually we need sMid from BEFORE the decode, not after. Let me fix this.

	// For now, just show the libopus 48kHz output for comparison
	t.Log("\nLibopus 48kHz samples (first 20):")
	for i := 0; i < 20 && i < libN; i++ {
		t.Logf("  [%2d] %+.6f (int16: %6d)", i, libOut[i], int16(libOut[i]*32768))
	}

	// Calculate what the expected ratio is
	// MB native is 12kHz, output is 48kHz, so 240 native → 960 output (4x)
	// The first few output samples depend on sMid and first few native samples
	expectedNative := config.SampleRate * 20 / 1000 // 240 for MB
	expected48k := 48000 * 20 / 1000                // 960
	t.Logf("\nExpected: %d native → %d @ 48kHz (ratio %.2f)", expectedNative, expected48k, float64(expected48k)/float64(expectedNative))

	// Quick SNR check on 48kHz output
	// We need to decode using gopus Opus decoder for fair comparison
	goDec, _ := gopus.NewDecoder(48000, 1)
	// Process packets 0-136
	for i := 0; i < 137; i++ {
		goDec.DecodeFloat32(packets[i])
	}
	// Now decode packet 137
	goOut, _ := goDec.DecodeFloat32(pkt)

	minLen := len(goOut)
	if libN < minLen {
		minLen = libN
	}

	var sumSqErr, sumSqSig float64
	for j := 0; j < minLen; j++ {
		diff := goOut[j] - libOut[j]
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(libOut[j] * libOut[j])
	}
	snr := 10 * math.Log10(sumSqSig/sumSqErr)

	t.Logf("\n48kHz comparison: SNR=%.1f dB", snr)
	t.Log("\nFirst 10 samples at 48kHz:")
	for i := 0; i < 10 && i < minLen; i++ {
		diff := goOut[i] - libOut[i]
		t.Logf("  [%2d] go=%+.6f lib=%+.6f diff=%+.6f", i, goOut[i], libOut[i], diff)
	}
}
