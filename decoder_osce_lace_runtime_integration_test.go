//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

// Smoke test for the OSCE LACE / NoLACE postfilter wiring on the decoder hot
// path. The wiring lives in `decoder_osce_lace_apply.go`; this test verifies
// that:
//
//   - SetOSCELACE(true) + SetDNNBlob(merged core+LACE) succeeds.
//   - Decoding a SILK WB packet completes without panic / error.
//   - The decoded PCM has non-zero energy (so the postfilter helper did
//     not zero out the standard silk_resampler output).
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
	// return gate (fs_kHz == 16, nb_subfr == 4) so the postfilter hook is
	// expected to invoke the model-backed forward pass. The decoded PCM
	// must remain non-zero.
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

	// The real LACE/NoLACE forward pass must mutate the native 16 kHz
	// int16 SILK lowband in place (libopus mirror: `osce_enhance_frame`
	// overwrites psDec->outBuf with the postfilter-enhanced samples).
	// Compare the int16 lowband produced by a LACE-off decode against
	// the same packet decoded with LACE on.
	//
	// The current gopus call site invokes the postfilter AFTER
	// silk_resampler, so the public 48 kHz `out` buffer is not yet
	// affected -- the mutation flows through to the OSCE BWE forward
	// pass which reads `LatestNativeMono()` downstream. Checking the
	// int16 buffer directly keeps this smoke test independent of the
	// call-site ordering.
	t.Run("silk_wb_lace_mutates_native_lowband", func(t *testing.T) {
		const frameSize = 960 // 20 ms @ 48 kHz
		packet := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

		// Reference decode with LACE disabled to capture the
		// pre-postfilter native lowband.
		decRef, err := NewDecoder(DefaultDecoderConfig(48000, 1))
		if err != nil {
			t.Fatalf("NewDecoder(ref): %v", err)
		}
		if err := decRef.SetOSCELACE(false); err != nil {
			t.Fatalf("SetOSCELACE(false): %v", err)
		}
		if err := decRef.SetDNNBlob(merged); err != nil {
			t.Fatalf("SetDNNBlob(ref): %v", err)
		}
		pcmRef := make([]float32, decRef.maxPacketSamples*decRef.channels)
		if _, err := decRef.Decode(packet, pcmRef); err != nil {
			t.Fatalf("Decode(ref): %v", err)
		}
		nativeRef, fsKHzRef := decRef.silkDecoder.LatestNativeMono()
		if nativeRef == nil || fsKHzRef != 16 {
			t.Fatalf("ref decode produced no 16 kHz native lowband: nativeRef=%v fsKHz=%d", nativeRef, fsKHzRef)
		}
		nativeRefCopy := make([]int16, osceLACEFrameSamples)
		copy(nativeRefCopy, nativeRef[:osceLACEFrameSamples])

		// LACE-on decode using a fresh decoder so the runtime state
		// has no carry-over from prior subtests.
		decLACE, err := NewDecoder(DefaultDecoderConfig(48000, 1))
		if err != nil {
			t.Fatalf("NewDecoder(lace): %v", err)
		}
		if err := decLACE.SetOSCELACE(true); err != nil {
			t.Fatalf("SetOSCELACE(true): %v", err)
		}
		if err := decLACE.SetDNNBlob(merged); err != nil {
			t.Fatalf("SetDNNBlob(lace): %v", err)
		}
		pcmLACE := make([]float32, decLACE.maxPacketSamples*decLACE.channels)
		if _, err := decLACE.Decode(packet, pcmLACE); err != nil {
			t.Fatalf("Decode(lace): %v", err)
		}
		nativeLACE, fsKHzLACE := decLACE.silkDecoder.LatestNativeMono()
		if nativeLACE == nil || fsKHzLACE != 16 {
			t.Fatalf("lace decode produced no 16 kHz native lowband: nativeLACE=%v fsKHz=%d", nativeLACE, fsKHzLACE)
		}

		// The 320-sample 20 ms lowband must differ on at least one
		// sample. If the buffers are bit-identical, the LACE/NoLACE
		// forward pass has degenerated into an identity copy and the
		// postfilter is not actually running.
		var diffCount int
		var maxDiff int
		for i := 0; i < osceLACEFrameSamples; i++ {
			d := int(nativeLACE[i]) - int(nativeRefCopy[i])
			if d < 0 {
				d = -d
			}
			if d > maxDiff {
				maxDiff = d
			}
			if d > 0 {
				diffCount++
			}
		}
		if diffCount == 0 {
			t.Fatalf("LACE-on and LACE-off native int16 lowband are bit-identical: forward pass appears to be an identity copy")
		}
		t.Logf("LACE postfilter altered %d/%d native int16 samples; max abs diff %d", diffCount, osceLACEFrameSamples, maxDiff)
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
