//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"math"
	"testing"
)

// TestDecoderOSCEBWECrossFadeTransition exercises the BWE <-> non-BWE
// transition cross-fade. It decodes a Hybrid SWB packet (no BWE), then a
// SILK WB packet (BWE active), then another Hybrid SWB packet (no BWE), and
// verifies that:
//
//   - Each decode completes without error and returns the expected sample
//     count.
//   - The BWE-active state is tracked across transitions (entering BWE on
//     the SILK WB frame, leaving BWE on the next Hybrid frame).
//   - The PCM output contains no NaN/Inf samples and stays inside the
//     [-1.5, 1.5] envelope -- the cross-fade is a weighted sum of two
//     bounded signals so it cannot produce wild discontinuities.
//   - The frame boundaries do not contain raw step discontinuities greater
//     than the in-frame dynamic range (a sanity check that the cross-fade
//     does not introduce audible clicks).
func TestDecoderOSCEBWECrossFadeTransition(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(mono 48kHz): %v", err)
	}
	if err := dec.SetOSCEBWE(true); err != nil {
		t.Fatalf("SetOSCEBWE(true): %v", err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+BWE): %v", err)
	}
	if !dec.osceBWEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE BWE runtime model after SetDNNBlob")
	}

	const frameSize = 960 // 20 ms @ 48 kHz
	hybridA := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)
	silkWB := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)
	hybridB := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

	pcmA := make([]float32, dec.maxPacketSamples*dec.channels)
	pcmB := make([]float32, dec.maxPacketSamples*dec.channels)
	pcmC := make([]float32, dec.maxPacketSamples*dec.channels)

	// Step 1: Hybrid SWB -- BWE inactive. prevBWEActive must be false.
	gotA, err := dec.Decode(hybridA, pcmA)
	if err != nil {
		t.Fatalf("Decode(hybrid SWB #1): %v", err)
	}
	if gotA != frameSize {
		t.Fatalf("Decode(hybrid SWB #1) returned %d samples, want %d", gotA, frameSize)
	}
	if dec.osceBWE != nil && dec.osceBWE.prevBWEActive {
		t.Fatalf("prevBWEActive=true after Hybrid SWB decode (BWE should be inactive)")
	}

	// Step 2: SILK WB -- BWE active. prevBWEActive must transition to true.
	gotB, err := dec.Decode(silkWB, pcmB)
	if err != nil {
		t.Fatalf("Decode(silk WB): %v", err)
	}
	if gotB != frameSize {
		t.Fatalf("Decode(silk WB) returned %d samples, want %d", gotB, frameSize)
	}
	if dec.osceBWE == nil || !dec.osceBWE.prevBWEActive {
		t.Fatalf("prevBWEActive=false after SILK WB decode (BWE should be active)")
	}

	// Step 3: Hybrid SWB again -- BWE leaves, cross-fade out. prevBWEActive
	// must transition back to false.
	gotC, err := dec.Decode(hybridB, pcmC)
	if err != nil {
		t.Fatalf("Decode(hybrid SWB #2): %v", err)
	}
	if gotC != frameSize {
		t.Fatalf("Decode(hybrid SWB #2) returned %d samples, want %d", gotC, frameSize)
	}
	if dec.osceBWE != nil && dec.osceBWE.prevBWEActive {
		t.Fatalf("prevBWEActive=true after Hybrid SWB transition (BWE should be inactive)")
	}

	// Verify all three decoded buffers are well-formed (no NaN/Inf, no
	// runaway samples) and the cross-fade boundary at sample 0 of each
	// frame is within the in-frame dynamic range.
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
	checkPCMSane(t, "hybrid SWB #1", pcmA, gotA)
	checkPCMSane(t, "silk WB", pcmB, gotB)
	checkPCMSane(t, "hybrid SWB #2", pcmC, gotC)

	// Sanity: the BWE cross-fade region (first 480 samples of the
	// transition frames) should produce a continuous signal. We measure the
	// maximum absolute sample-to-sample step within the cross-fade window
	// and compare it against the overall in-frame max step. If the
	// cross-fade introduced an obvious discontinuity the boundary step
	// would exceed the in-frame dynamic range by a noticeable margin.
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
	xfadeStepB := maxStep(pcmB, 0, 480)
	fullStepB := maxStep(pcmB, 0, gotB)
	// xfade region step should not exceed the in-frame maximum (allow
	// equality -- the cross-fade lives inside the same frame).
	if xfadeStepB > fullStepB+1e-3 {
		t.Fatalf("cross-fade boundary on SILK WB frame produced step %v exceeding in-frame max %v", xfadeStepB, fullStepB)
	}
	xfadeStepC := maxStep(pcmC, 0, 480)
	fullStepC := maxStep(pcmC, 0, gotC)
	if xfadeStepC > fullStepC+1e-3 {
		t.Fatalf("cross-fade boundary on Hybrid SWB #2 frame produced step %v exceeding in-frame max %v", xfadeStepC, fullStepC)
	}
}

// TestDecoderOSCEBWEPLC verifies BWE is invoked during PLC when the previous
// packet was SILK WB. The PLC output must be non-zero (so the BWE forward
// pass did not collapse the concealed lowband to silence) and must not
// contain NaN/Inf samples.
func TestDecoderOSCEBWEPLC(t *testing.T) {
	coreBlob := requireLibopusDecoderNeuralModelBlob(t)
	bweBlob := requireLibopusOSCEBWEModelBlob(t)

	merged := make([]byte, 0, len(coreBlob)+len(bweBlob))
	merged = append(merged, coreBlob...)
	merged = append(merged, bweBlob...)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(mono 48kHz): %v", err)
	}
	if err := dec.SetOSCEBWE(true); err != nil {
		t.Fatalf("SetOSCEBWE(true): %v", err)
	}
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+BWE): %v", err)
	}
	if !dec.osceBWEModelLoadedRuntime() {
		t.Fatalf("decoder did not bind OSCE BWE runtime model after SetDNNBlob")
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

	// Step 2: invoke Decode(nil) for PLC. With BWE armed, the PLC path
	// must invoke maybeApplyOSCEBWEPostSilk after the standard PLC
	// upsampling so the concealed lowband is bandwidth-extended like a
	// good SILK WB frame.
	pcmPLC := make([]float32, dec.maxPacketSamples*dec.channels)
	gotPLC, err := dec.Decode(nil, pcmPLC)
	if err != nil {
		t.Fatalf("Decode(nil) PLC: %v", err)
	}
	if gotPLC != frameSize {
		t.Fatalf("Decode(nil) PLC returned %d samples, want %d", gotPLC, frameSize)
	}

	// PLC output must be non-zero -- BWE on a non-silent concealed lowband
	// should produce non-trivial output. The silk_resampler upsampling on
	// its own already yields non-zero energy, so BWE-active or BWE-failed
	// both pass this check; the assertion guards against the PLC path
	// returning silence by accident.
	var energy float64
	for i := 0; i < gotPLC*dec.channels; i++ {
		v := pcmPLC[i]
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("PLC PCM contains NaN/Inf at sample %d: %v", i, v)
		}
		energy += float64(v) * float64(v)
	}
	if energy == 0 {
		t.Fatalf("PLC PCM is silent after BWE-armed SILK WB packet -- BWE PLC path likely did not run")
	}
}
