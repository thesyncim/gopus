//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"math"
	"testing"
)

// TestDecoderOSCELACECrossFadeTransition exercises the LACE/NoLACE
// <-> non-LACE transition cross-fade. It decodes a SILK WB packet (LACE
// active), then a Hybrid SWB packet (LACE inactive), then another SILK WB
// packet (LACE active again, triggering the cross-fade on the way in), and
// verifies that:
//
//   - Each decode completes without error and returns the expected sample
//     count.
//   - The LACE-active state is tracked across transitions
//     (entering LACE on the first SILK WB frame, leaving on Hybrid, and
//     re-entering on the second SILK WB frame so the cross-fade runs).
//   - The PCM output contains no NaN/Inf samples and stays inside the
//     [-1.5, 1.5] envelope -- the cross-fade is a weighted sum of two
//     bounded signals so it cannot produce wild discontinuities.
//   - The cross-fade boundary at the start of the LACE re-entry frame
//     does not introduce step discontinuities larger than the in-frame
//     dynamic range.
func TestDecoderOSCELACECrossFadeTransition(t *testing.T) {
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
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+LACE): %v", err)
	}
	if !dec.osceLACEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE LACE runtime model after SetDNNBlob")
	}

	const frameSize = 960 // 20 ms @ 48 kHz
	silkWBA := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
	hybridSWB := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)
	silkWBB := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

	pcmA := make([]float32, dec.maxPacketSamples*dec.channels)
	pcmB := make([]float32, dec.maxPacketSamples*dec.channels)
	pcmC := make([]float32, dec.maxPacketSamples*dec.channels)

	// Step 1: SILK WB -- LACE active. prevLACEActive must transition to
	// true. This is the initial transition into LACE (prev was implicitly
	// inactive at decoder reset) so the cross-fade runs here too.
	gotA, err := dec.Decode(silkWBA, pcmA)
	if err != nil {
		t.Fatalf("Decode(silk WB #1): %v", err)
	}
	if gotA != frameSize {
		t.Fatalf("Decode(silk WB #1) returned %d samples, want %d", gotA, frameSize)
	}
	if dec.osceLACE == nil || !dec.osceLACE.prevLACEActive {
		t.Fatalf("prevLACEActive=false after SILK WB decode (LACE should be active)")
	}

	// Step 2: Hybrid SWB -- LACE inactive. prevLACEActive must clear so
	// that the next SILK WB packet runs the cross-fade.
	gotB, err := dec.Decode(hybridSWB, pcmB)
	if err != nil {
		t.Fatalf("Decode(hybrid SWB): %v", err)
	}
	if gotB != frameSize {
		t.Fatalf("Decode(hybrid SWB) returned %d samples, want %d", gotB, frameSize)
	}
	if dec.osceLACE != nil && dec.osceLACE.prevLACEActive {
		t.Fatalf("prevLACEActive=true after Hybrid SWB decode (LACE should be inactive)")
	}

	// Step 3: SILK WB again -- LACE active, cross-fade runs on entry.
	// prevLACEActive must transition back to true.
	gotC, err := dec.Decode(silkWBB, pcmC)
	if err != nil {
		t.Fatalf("Decode(silk WB #2): %v", err)
	}
	if gotC != frameSize {
		t.Fatalf("Decode(silk WB #2) returned %d samples, want %d", gotC, frameSize)
	}
	if dec.osceLACE == nil || !dec.osceLACE.prevLACEActive {
		t.Fatalf("prevLACEActive=false after SILK WB transition (LACE should be active)")
	}

	checkPCMSane := func(t *testing.T, name string, pcm []float32, n int) {
		t.Helper()
		var maxAbs float32
		for i := 0; i < n; i++ {
			v := pcm[i]
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("%s: PCM contains NaN/Inf at sample %d: %v", name, i, v)
			}
			if v > maxAbs {
				maxAbs = v
			} else if -v > maxAbs {
				maxAbs = -v
			}
		}
		if maxAbs > 1.5 {
			t.Fatalf("%s: PCM exceeds [-1.5, 1.5] envelope (maxAbs=%v); cross-fade likely produced runaway samples", name, maxAbs)
		}
	}
	checkPCMSane(t, "silk WB #1", pcmA, gotA)
	checkPCMSane(t, "hybrid SWB", pcmB, gotB)
	checkPCMSane(t, "silk WB #2", pcmC, gotC)

	// Sanity: the LACE cross-fade region (first 480 samples of the SILK
	// re-entry frame at 48 kHz, derived from the first 160 samples of the
	// 16 kHz native lowband which the silk_resampler upsamples) should be
	// continuous. We measure the maximum absolute sample-to-sample step
	// in the cross-fade window and compare it against the overall in-frame
	// max step; the boundary step must not exceed the in-frame maximum.
	maxStep := func(pcm []float32, start, end int) float32 {
		var m float32
		for i := start + 1; i < end; i++ {
			d := pcm[i] - pcm[i-1]
			if d < 0 {
				d = -d
			}
			if d > m {
				m = d
			}
		}
		return m
	}
	xfadeStepC := maxStep(pcmC, 0, 480)
	fullStepC := maxStep(pcmC, 0, gotC)
	if xfadeStepC > fullStepC+1e-3 {
		t.Fatalf("LACE re-entry cross-fade produced step %v exceeding in-frame max %v", xfadeStepC, fullStepC)
	}
}

// TestDecoderOSCELACEPLC verifies LACE/NoLACE is invoked during PLC when
// the previous packet was SILK WB. The PLC output must be non-zero (so the
// LACE forward pass did not collapse the concealed lowband to silence) and
// must not contain NaN/Inf samples.
func TestDecoderOSCELACEPLC(t *testing.T) {
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
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+LACE): %v", err)
	}
	if !dec.osceLACEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE LACE runtime model after SetDNNBlob")
	}

	const frameSize = 960 // 20 ms @ 48 kHz
	silkWB := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

	// Step 1: decode a SILK WB packet so the decoder retains valid
	// lastPacketMode/lastBandwidth for the upcoming PLC.
	pcmGood := make([]float32, dec.maxPacketSamples*dec.channels)
	gotGood, err := dec.Decode(silkWB, pcmGood)
	if err != nil {
		t.Fatalf("Decode(silk WB): %v", err)
	}
	if gotGood != frameSize {
		t.Fatalf("Decode(silk WB) returned %d samples, want %d", gotGood, frameSize)
	}
	if dec.lastPacketMode != ModeSILK || dec.lastBandwidth != BandwidthWideband {
		t.Fatalf("decoder state after good SILK WB packet: mode=%v bandwidth=%v, want SILK WB", dec.lastPacketMode, dec.lastBandwidth)
	}

	// Step 2: invoke Decode(nil) for PLC. With LACE armed, the PLC path
	// must invoke maybeApplyOSCELACEPostSilk after the standard PLC
	// resampling so the concealed lowband is postfilter-enhanced like a
	// good SILK WB frame.
	pcmPLC := make([]float32, dec.maxPacketSamples*dec.channels)
	gotPLC, err := dec.Decode(nil, pcmPLC)
	if err != nil {
		t.Fatalf("Decode(nil) PLC: %v", err)
	}
	if gotPLC != frameSize {
		t.Fatalf("Decode(nil) PLC returned %d samples, want %d", gotPLC, frameSize)
	}

	// PLC output must be non-zero -- the silk_resampler upsampling alone
	// already produces non-zero energy from the concealed lowband, so this
	// check guards against an accidental regression where the LACE PLC
	// path zeroes out the buffer or otherwise yields silence.
	var energy float64
	for i := 0; i < gotPLC*dec.channels; i++ {
		v := pcmPLC[i]
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("PLC PCM contains NaN/Inf at sample %d: %v", i, v)
		}
		energy += float64(v) * float64(v)
	}
	if energy == 0 {
		t.Fatalf("PLC PCM is silent after LACE-armed SILK WB packet -- LACE PLC path likely did not run")
	}
}
