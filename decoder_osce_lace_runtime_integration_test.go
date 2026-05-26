//go:build gopus_extra_controls

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

func TestDecoderOSCELACEComplexityMode(t *testing.T) {
	for _, tc := range []struct {
		complexity int
		want       osceLACEMode
	}{
		{complexity: 5, want: osceLACEModeNone},
		{complexity: 6, want: osceLACEModeLACE},
		{complexity: 7, want: osceLACEModeNoLACE},
		{complexity: 10, want: osceLACEModeNoLACE},
	} {
		if got := pickOSCELACEMode(tc.complexity); got != tc.want {
			t.Fatalf("pickOSCELACEMode(%d)=%v want %v", tc.complexity, got, tc.want)
		}
	}
}

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
	if err := dec.SetComplexity(6); err != nil {
		t.Fatalf("SetComplexity(6): %v", err)
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

		pcm := make([]float32, dec.maxPacketSamples*int(dec.channels))
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

		pcm := make([]float32, dec.maxPacketSamples*int(dec.channels))
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

	// libopus reset semantics keep the first eligible LACE frame raw, then
	// cross-fade the second eligible frame before steady enhancement.
	// The current call site still runs after silk_resampler, so this checks
	// the native lowband that downstream OSCE BWE consumes.
	t.Run("silk_wb_lace_reset_then_mutates_native_lowband", func(t *testing.T) {
		const frameSize = 960 // 20 ms @ 48 kHz
		packetA := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
		packetB := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

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
		pcmRef := make([]float32, decRef.maxPacketSamples*int(decRef.channels))
		if _, err := decRef.Decode(packetA, pcmRef); err != nil {
			t.Fatalf("Decode(ref #1): %v", err)
		}
		pcmRefFirst := append([]float32(nil), pcmRef[:frameSize]...)
		nativeRef, fsKHzRef := decRef.silkDecoder.LatestNativeMono()
		if nativeRef == nil || fsKHzRef != 16 {
			t.Fatalf("ref decode produced no 16 kHz native lowband: nativeRef=%v fsKHz=%d", nativeRef, fsKHzRef)
		}
		nativeRefFirst := make([]int16, osceLACEFrameSamples)
		copy(nativeRefFirst, nativeRef[:osceLACEFrameSamples])
		if _, err := decRef.Decode(packetB, pcmRef); err != nil {
			t.Fatalf("Decode(ref #2): %v", err)
		}
		nativeRef, fsKHzRef = decRef.silkDecoder.LatestNativeMono()
		if nativeRef == nil || fsKHzRef != 16 {
			t.Fatalf("ref decode #2 produced no 16 kHz native lowband: nativeRef=%v fsKHz=%d", nativeRef, fsKHzRef)
		}
		nativeRefSecond := make([]int16, osceLACEFrameSamples)
		copy(nativeRefSecond, nativeRef[:osceLACEFrameSamples])

		decLACE, err := NewDecoder(DefaultDecoderConfig(48000, 1))
		if err != nil {
			t.Fatalf("NewDecoder(lace): %v", err)
		}
		if err := decLACE.SetComplexity(6); err != nil {
			t.Fatalf("SetComplexity(6): %v", err)
		}
		if err := decLACE.SetOSCELACE(true); err != nil {
			t.Fatalf("SetOSCELACE(true): %v", err)
		}
		if err := decLACE.SetDNNBlob(merged); err != nil {
			t.Fatalf("SetDNNBlob(lace): %v", err)
		}
		pcmLACE := make([]float32, decLACE.maxPacketSamples*int(decLACE.channels))
		if _, err := decLACE.Decode(packetA, pcmLACE); err != nil {
			t.Fatalf("Decode(lace #1): %v", err)
		}
		publicDiffCount, publicMaxDiff := float32BufferDiff(pcmLACE[:frameSize], pcmRefFirst)
		if publicDiffCount != 0 {
			t.Fatalf("first LACE-eligible public PCM differs from raw output after reset: diffCount=%d maxDiff=%g", publicDiffCount, publicMaxDiff)
		}
		nativeLACE, fsKHzLACE := decLACE.silkDecoder.LatestNativeMono()
		if nativeLACE == nil || fsKHzLACE != 16 {
			t.Fatalf("lace decode produced no 16 kHz native lowband: nativeLACE=%v fsKHz=%d", nativeLACE, fsKHzLACE)
		}
		diffCount, maxDiff := int16BufferDiff(nativeLACE[:osceLACEFrameSamples], nativeRefFirst)
		if diffCount != 0 {
			t.Fatalf("first LACE-eligible frame differs from raw output after reset: diffCount=%d maxDiff=%d", diffCount, maxDiff)
		}
		if decLACE.osceLACE == nil {
			t.Fatalf("LACE state is nil after first eligible frame")
		}
		if decLACE.osceLACE.laceResetFrames[0] != 1 {
			t.Fatalf("after first LACE frame reset countdown=%d, want 1", decLACE.osceLACE.laceResetFrames[0])
		}

		if _, err := decLACE.Decode(packetB, pcmLACE); err != nil {
			t.Fatalf("Decode(lace #2): %v", err)
		}
		nativeLACE, fsKHzLACE = decLACE.silkDecoder.LatestNativeMono()
		if nativeLACE == nil || fsKHzLACE != 16 {
			t.Fatalf("lace decode #2 produced no 16 kHz native lowband: nativeLACE=%v fsKHz=%d", nativeLACE, fsKHzLACE)
		}
		diffCount, maxDiff = int16BufferDiff(nativeLACE[:osceLACEFrameSamples], nativeRefSecond)
		if diffCount == 0 {
			t.Fatalf("second LACE-eligible frame is still raw: forward pass/reset cross-fade did not affect native lowband")
		}
		publicDiffCount, publicMaxDiff = float32BufferDiff(pcmLACE[:frameSize], pcmRef[:frameSize])
		if publicDiffCount == 0 {
			t.Fatalf("second LACE-eligible public PCM is still raw: native postfilter did not feed silk_resampler")
		}
		secondNativeDiff, secondNativeMax := diffCount, maxDiff
		secondPublicDiff, secondPublicMax := publicDiffCount, publicMaxDiff
		if decLACE.osceLACE.laceResetFrames[0] != 0 {
			t.Fatalf("after second LACE frame reset countdown=%d, want 0", decLACE.osceLACE.laceResetFrames[0])
		}
		decLACE.Reset()
		if _, err := decLACE.Decode(packetA, pcmLACE); err != nil {
			t.Fatalf("Decode(lace after Reset): %v", err)
		}
		publicDiffCount, publicMaxDiff = float32BufferDiff(pcmLACE[:frameSize], pcmRefFirst)
		if publicDiffCount != 0 {
			t.Fatalf("post-Reset LACE public PCM differs from raw output: diffCount=%d maxDiff=%g", publicDiffCount, publicMaxDiff)
		}
		nativeLACE, fsKHzLACE = decLACE.silkDecoder.LatestNativeMono()
		if nativeLACE == nil || fsKHzLACE != 16 {
			t.Fatalf("lace post-Reset decode produced no 16 kHz native lowband: nativeLACE=%v fsKHz=%d", nativeLACE, fsKHzLACE)
		}
		diffCount, maxDiff = int16BufferDiff(nativeLACE[:osceLACEFrameSamples], nativeRefFirst)
		if diffCount != 0 {
			t.Fatalf("post-Reset LACE native frame differs from raw output: diffCount=%d maxDiff=%d", diffCount, maxDiff)
		}
		if decLACE.osceLACE.laceResetFrames[0] != 1 {
			t.Fatalf("after post-Reset LACE frame reset countdown=%d, want 1", decLACE.osceLACE.laceResetFrames[0])
		}
		t.Logf("LACE postfilter altered %d/%d native int16 samples (max %d) and %d/%d public PCM samples (max %g)",
			secondNativeDiff, osceLACEFrameSamples, secondNativeMax, secondPublicDiff, frameSize, secondPublicMax)
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
		pcm := make([]float32, dec.maxPacketSamples*int(dec.channels))
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

func int16BufferDiff(a, b []int16) (count, maxAbs int) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		if d > 0 {
			count++
			if d > maxAbs {
				maxAbs = d
			}
		}
	}
	count += len(a) - n
	count += len(b) - n
	return count, maxAbs
}

func float32BufferDiff(a, b []float32) (count int, maxAbs float32) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		d := a[i] - b[i]
		if d < 0 {
			d = -d
		}
		if d > 0 {
			count++
			if d > maxAbs {
				maxAbs = d
			}
		}
	}
	count += len(a) - n
	count += len(b) - n
	return count, maxAbs
}
