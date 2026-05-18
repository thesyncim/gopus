//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

// Phase 1 smoke test for the OSCE LACE / NoLACE postfilter wiring on the
// decoder hot path. The wiring lives in `decoder_osce_lace_apply.go`; this
// test verifies that:
//
//   - SetOSCELACE(true) + SetDNNBlob(merged core+LACE) succeeds.
//   - Decoding a SILK WB packet completes without panic / error.
//   - The decoded PCM has non-zero energy (so the postfilter helper did
//     not zero out the standard silk_resampler output when conditions
//     are not met or when the Phase 1 identity stub runs).
//   - Hybrid SWB packets bypass the postfilter (libopus only runs LACE /
//     NoLACE for SILK-only mode at 16 kHz internal).
//
// The test skips cleanly when the libopus helper binaries cannot be built
// (e.g. missing libopus tarball or compiler), matching the OSCE BWE
// runtime integration smoke test.

import (
	"math"
	"testing"
)

func TestDecoderOSCELACERuntimeIntegration(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	laceBlob := requireLibopusOSCELACEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(laceBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, laceBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(mono 48kHz): %v", err)
	}

	if err := dec.SetOSCELACE(true); err != nil {
		t.Fatalf("SetOSCELACE(true): %v", err)
	}
	enabled, err := dec.OSCELACE()
	if err != nil {
		t.Fatalf("OSCELACE(): %v", err)
	}
	if !enabled {
		t.Fatalf("OSCELACE() == false after SetOSCELACE(true)")
	}

	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+LACE): %v", err)
	}
	if !dec.osceModelsLoaded {
		t.Fatalf("decoder did not retain osceModelsLoaded after SetDNNBlob")
	}
	if !dec.osceLACEModelLoaded {
		t.Fatalf("decoder did not set osceLACEModelLoaded after SetDNNBlob")
	}
	if !dec.osceLACEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE LACE runtime model after SetDNNBlob")
	}

	// SILK WB packets satisfy the libopus `osce_enhance_frame` early-
	// return gate (fs_kHz == 16, nb_subfr == 4) so the postfilter hook
	// is expected to invoke the Phase 1 identity stub. The decoded PCM
	// must remain non-zero -- the identity copy must not clobber the
	// standard silk_resampler output to silence.
	t.Run("silk_wb_invokes_lace", func(t *testing.T) {
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
			t.Fatalf("decoded PCM is silent after LACE-armed SILK WB decode -- standard silk_resampler output was unexpectedly clobbered")
		}
	})

	// Hybrid SWB packets do NOT satisfy the libopus LACE/NoLACE gate
	// (mode != MODE_SILK_ONLY); the postfilter helper must short-circuit
	// and leave the standard hybrid upsampler output untouched.
	t.Run("hybrid_swb_bypasses_lace", func(t *testing.T) {
		dec.Reset()
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

		var energy float64
		for _, v := range pcm[:got*dec.channels] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("decoded PCM contains NaN/Inf: %v", v)
			}
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("decoded PCM is silent after LACE-armed Hybrid SWB decode -- standard hybrid output was unexpectedly clobbered by the postfilter bypass branch")
		}
	})

	// SetOSCELACE(false) disables the helper at runtime; the SILK WB
	// decode must still produce non-zero audio with the postfilter off.
	t.Run("disable_keeps_decode_working", func(t *testing.T) {
		if err := dec.SetOSCELACE(false); err != nil {
			t.Fatalf("SetOSCELACE(false): %v", err)
		}
		enabled, err := dec.OSCELACE()
		if err != nil {
			t.Fatalf("OSCELACE(): %v", err)
		}
		if enabled {
			t.Fatalf("OSCELACE() == true after SetOSCELACE(false)")
		}

		dec.Reset()
		const frameSize = 960
		packet := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
		pcm := make([]float32, dec.maxPacketSamples*dec.channels)
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode(silk WB packet) after disable: %v", err)
		}
		if got != frameSize {
			t.Fatalf("Decode returned %d samples, want %d", got, frameSize)
		}
		var energy float64
		for _, v := range pcm[:got*dec.channels] {
			energy += float64(v) * float64(v)
		}
		if energy == 0 {
			t.Fatalf("decoded PCM silent with LACE disabled")
		}
	})
}
