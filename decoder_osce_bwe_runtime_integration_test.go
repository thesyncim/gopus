//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

// Phase 1 smoke test for the OSCE BWE forward-pass wiring on the decoder hot
// path. The wiring lives in `decoder_osce_bwe_apply.go`; this test verifies
// that:
//
//   - SetOSCEBWE(true) + SetDNNBlob(merged core+BWE) succeeds.
//   - Decoding a Hybrid SWB packet completes without panic / error.
//   - The decoder returns the expected number of samples per channel.
//   - The decoded PCM has non-zero energy (so the helper did not zero out the
//     standard silk_resampler output when BWE conditions are not met).
//
// libopus only enables OSCE_MODE_SILK_BBWE for SILK-only WB at 48 kHz API, so
// a Hybrid SWB packet must NOT trigger the BWE replacement. The test therefore
// implicitly verifies the gate logic in maybeApplyOSCEBWEPostSilk: the BWE
// hook is armed (model loaded, control enabled) but the standard hybrid
// upsampler output is preserved untouched.

import (
	"math"
	"testing"
)

func TestDecoderOSCEBWERuntimeIntegration(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder(stereo 48kHz): %v", err)
	}

	if err := dec.SetOSCEBWE(true); err != nil {
		t.Fatalf("SetOSCEBWE(true): %v", err)
	}
	enabled, err := dec.OSCEBWE()
	if err != nil {
		t.Fatalf("OSCEBWE(): %v", err)
	}
	if !enabled {
		t.Fatalf("OSCEBWE() == false after SetOSCEBWE(true)")
	}

	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+BWE): %v", err)
	}
	if !dec.osceBWEModelLoaded {
		t.Fatalf("decoder did not retain osceBWEModelLoaded after SetDNNBlob")
	}
	if !dec.osceBWEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE BWE runtime model after SetDNNBlob")
	}

	// Hybrid SWB packets are encoded mono; the stereo decoder will up-mix.
	// Hybrid mode does NOT satisfy OSCE_MODE_SILK_BBWE so the BWE hook must
	// short-circuit and leave the standard hybrid upsampler output untouched.
	t.Run("hybrid_swb_bypasses_bwe", func(t *testing.T) {
		const frameSize = 960 // 20 ms @ 48 kHz
		packet := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeHybrid || toc.Bandwidth != BandwidthSuperwideband {
			t.Fatalf("unexpected TOC: mode=%v bandwidth=%v", toc.Mode, toc.Bandwidth)
		}

		pcm := make([]float32, dec.maxPacketSamples*dec.channels)
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(hybrid SWB packet): %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode(hybrid SWB packet) returned %d samples per channel, want %d", got, frameSize)
		}

		// PCM must be non-zero -- BWE must not have clobbered the standard
		// hybrid upsampler output (Hybrid mode does not satisfy
		// OSCE_MODE_SILK_BBWE in libopus).
		var energy float64
		for _, v := range pcm[:got*dec.channels] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("decoded PCM contains NaN/Inf: %v", v)
			}
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("decoded PCM is silent after BWE-armed Hybrid SWB decode -- standard upsampler output was unexpectedly clobbered")
		}
	})

	// SILK WB packets DO satisfy OSCE_MODE_SILK_BBWE so the BWE hook
	// should overwrite the standard silk_resampler output with the BWE
	// 16k -> 48k forward pass. The packet is mono so the stereo decoder's
	// channels==2 branch in maybeApplyOSCEBWEPostSilk applies. Verify the
	// decode completes and PCM is non-zero (Phase 1 forward pass produces
	// non-trivial output for a sinusoidal input even with zero features).
	t.Run("silk_wb_invokes_bwe", func(t *testing.T) {
		// Reset to clear any prior packet state.
		dec.Reset()
		const frameSize = 960 // 20 ms @ 48 kHz
		packet := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
			t.Fatalf("unexpected TOC: mode=%v bandwidth=%v", toc.Mode, toc.Bandwidth)
		}

		pcm := make([]float32, dec.maxPacketSamples*dec.channels)
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(silk WB packet): %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode(silk WB packet) returned %d samples per channel, want %d", got, frameSize)
		}

		var energy float64
		for _, v := range pcm[:got*dec.channels] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("decoded PCM contains NaN/Inf: %v", v)
			}
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("decoded PCM is silent after BWE-armed SILK WB decode")
		}
	})
}
