//go:build gopus_osce

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecoderOSCEFloatToInt16MatchesLibopusScaleOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   float32
		want int16
	}{
		{name: "positive clamp", in: 1.5, want: 32767},
		{name: "negative clamp", in: -1.5, want: -32767},
		{name: "negative full scale", in: -1.0, want: -32767},
		{name: "half tie to even", in: float32(0.5 / 32768.0), want: 0},
		{name: "one point five tie to even", in: float32(1.5 / 32768.0), want: 2},
		{name: "two point five tie to even", in: float32(2.5 / 32768.0), want: 2},
		{name: "negative one point five tie to even", in: float32(-1.5 / 32768.0), want: -2},
	} {
		if got := osceFloatToInt16(tc.in); got != tc.want {
			t.Fatalf("%s: osceFloatToInt16(%g)=%d want %d", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestDecoderOSCELACECrossFadeTransition exercises LACE state across
// SILK-containing packets. It verifies that:
//
//   - Each decode completes without error and returns the expected sample
//     count.
//   - The LACE-active state remains set across SILK WB and Hybrid frames,
//     while the Hybrid frame consumes the pending reset cross-fade.
//   - The PCM output contains no NaN/Inf samples and stays inside the
//     [-1.5, 1.5] envelope -- the cross-fade is a weighted sum of two
//     bounded signals so it cannot produce wild discontinuities.
//   - The cross-fade boundary at the start of the Hybrid frame does not
//     introduce a step discontinuity larger than the in-frame dynamic range.
func osceLACETransitionPackets(t *testing.T) [][]byte {
	t.Helper()
	const frameSize = 960
	return [][]byte{
		makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband),
		makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband),
		makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband),
		makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband),
	}
}

func newOSCELACETransitionTestDecoder(t *testing.T) *Decoder {
	t.Helper()
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
	if err := dec.SetDNNBlob(merged); err != nil {
		t.Fatalf("SetDNNBlob(merged core+LACE): %v", err)
	}
	if !dec.osceLACEModelLoadedRuntime() {
		t.Fatal("decoder did not bind OSCE LACE runtime model after SetDNNBlob")
	}
	return dec
}

func TestDecoderOSCELACECrossFadeTransition(t *testing.T) {
	const frameSize = 960 // 20 ms @ 48 kHz
	packets := osceLACETransitionPackets(t)
	silkWBA, hybridSWB, silkWBB, silkWBC := packets[0], packets[1], packets[2], packets[3]
	dec := newOSCELACETransitionTestDecoder(t)

	pcmA := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	pcmB := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	pcmC := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	pcmD := make([]float32, dec.maxPacketSamples*int(dec.Channels()))

	// Step 1: SILK WB -- LACE active. prevLACEActive transitions to true.
	// libopus keeps this first eligible frame raw after reset and leaves one
	// reset frame pending for the next SILK-containing frame.
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
	if dec.osceLACE.laceResetFrames[0] != 1 {
		t.Fatalf("reset countdown after first SILK WB=%d want 1", dec.osceLACE.laceResetFrames[0])
	}

	// Step 2: Hybrid SWB still decodes a 16 kHz SILK low band. Its LACE
	// transition consumes the pending 10 ms cross-fade.
	gotB, err := dec.Decode(hybridSWB, pcmB)
	if err != nil {
		t.Fatalf("Decode(hybrid SWB): %v", err)
	}
	if gotB != frameSize {
		t.Fatalf("Decode(hybrid SWB) returned %d samples, want %d", gotB, frameSize)
	}
	if dec.osceLACE == nil || !dec.osceLACE.prevLACEActive {
		t.Fatal("Hybrid SWB did not retain active LACE state for its SILK low band")
	}
	if dec.osceLACE.laceResetFrames[0] != 0 {
		t.Fatalf("reset countdown after Hybrid SWB=%d want 0 after the LACE cross-fade", dec.osceLACE.laceResetFrames[0])
	}

	// Step 3: SILK WB again continues the same active LACE stream without
	// restarting the reset/cross-fade sequence.
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
	if dec.osceLACE.laceResetFrames[0] != 0 {
		t.Fatalf("reset countdown after SILK continuation=%d want 0", dec.osceLACE.laceResetFrames[0])
	}

	// Step 4: consecutive SILK WB continues with LACE after the reset
	// cross-fade was consumed by the Hybrid frame.
	gotD, err := dec.Decode(silkWBC, pcmD)
	if err != nil {
		t.Fatalf("Decode(silk WB #3): %v", err)
	}
	if gotD != frameSize {
		t.Fatalf("Decode(silk WB #3) returned %d samples, want %d", gotD, frameSize)
	}
	if dec.osceLACE.laceResetFrames[0] != 0 {
		t.Fatalf("reset countdown after SILK continuation=%d want 0", dec.osceLACE.laceResetFrames[0])
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
	checkPCMSane(t, "silk WB #3", pcmD, gotD)

	// Sanity: the LACE cross-fade region (first 480 samples of the Hybrid
	// frame at 48 kHz, derived from the first 160 samples of the 16 kHz
	// native lowband which the silk_resampler upsamples) should be continuous.
	// The boundary step must not exceed the in-frame maximum.
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
	if xfadeStepB > fullStepB+1e-3 {
		t.Fatalf("Hybrid LACE cross-fade produced step %v exceeding in-frame max %v", xfadeStepB, fullStepB)
	}
}

// TestDecoderOSCELACEHybridTransitionMatchesSelectedLibopus checks the
// transition against the matching OSCE-enabled libopus build. The C
// silk/decode_frame.c success path calls osce_enhance_frame for Hybrid frames
// after decoding their 16 kHz SILK low band.
func TestDecoderOSCELACEHybridTransitionMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	binPath, err := getLibopusOSCEDecodeSingleHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE decode single", err)
	}

	const (
		sampleRate = 48000
		frameSize  = 960
	)
	packets := osceLACETransitionPackets(t)
	libopusPCM, err := runLibopusOSCEDecodeSingle(binPath, sampleRate, 1, frameSize, 6, false, packets)
	if err != nil {
		t.Fatalf("selected libopus OSCE decode (complexity 6): %v", err)
	}
	libopusWithoutLACE, err := runLibopusOSCEDecodeSingle(binPath, sampleRate, 1, frameSize, 5, false, packets)
	if err != nil {
		t.Fatalf("selected libopus OSCE decode (complexity 5): %v", err)
	}
	wantSamples := frameSize * len(packets)
	if len(libopusPCM) != wantSamples || len(libopusWithoutLACE) != wantSamples {
		t.Fatalf("selected libopus samples: LACE=%d NoLACE=%d want %d each", len(libopusPCM), len(libopusWithoutLACE), wantSamples)
	}

	const hybridFrame = 1
	changedHybridSamples := 0
	for i := hybridFrame * frameSize; i < (hybridFrame+1)*frameSize; i++ {
		if math.Float32bits(libopusPCM[i]) != math.Float32bits(libopusWithoutLACE[i]) {
			changedHybridSamples++
		}
	}
	if changedHybridSamples == 0 {
		t.Fatal("selected libopus LACE output did not change any Hybrid SWB samples")
	}
	t.Logf("selected libopus LACE changes %d/%d Hybrid SWB samples", changedHybridSamples, frameSize)

	dec := newOSCELACETransitionTestDecoder(t)
	pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	for frame, packet := range packets {
		got, err := dec.Decode(packet, pcm)
		if err != nil {
			t.Fatalf("Decode packet %d: %v", frame, err)
		}
		if got != frameSize {
			t.Fatalf("Decode packet %d returned %d samples, want %d", frame, got, frameSize)
		}
		for sample := 0; sample < frameSize; sample++ {
			index := frame*frameSize + sample
			if gotBits, wantBits := math.Float32bits(pcm[sample]), math.Float32bits(libopusPCM[index]); gotBits != wantBits {
				t.Fatalf("packet %d sample %d: Go=%08x selected libopus=%08x", frame, sample, gotBits, wantBits)
			}
		}
	}
}

// TestDecoderOSCELACEPLC verifies LACE/NoLACE follows libopus PLC semantics:
// the lost SILK frame resets postfilter state instead of running
// osce_enhance_frame. The PLC output must be non-zero and must not contain
// NaN/Inf samples.
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
	if err := dec.SetComplexity(6); err != nil {
		t.Fatalf("SetComplexity(6): %v", err)
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
	pcmGood := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
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
	// must reset the postfilter state instead of enhancing the concealed
	// frame, matching libopus silk_decode_frame lost-branch behavior.
	pcmPLC := make([]float32, frameSize*dec.Channels())
	gotPLC, err := dec.Decode(nil, pcmPLC)
	if err != nil {
		t.Fatalf("Decode(nil) PLC: %v", err)
	}
	if gotPLC != frameSize {
		t.Fatalf("Decode(nil) PLC returned %d samples, want %d", gotPLC, frameSize)
	}

	if dec.osceLACE == nil || dec.osceLACE.laceResetFrames[0] != 2 || dec.osceLACE.laceMethod != osceLACEModeLACE {
		t.Fatal("SILK WB PLC must retain LACE and arm its two-frame output reset")
	}

	// PLC output must be non-zero -- the silk_resampler upsampling alone
	// already produces non-zero energy from the concealed lowband, so this
	// check guards against an accidental regression where the PLC path zeroes
	// out the buffer or otherwise yields silence.
	var energy float64
	for i := 0; i < gotPLC*dec.Channels(); i++ {
		v := pcmPLC[i]
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("PLC PCM contains NaN/Inf at sample %d: %v", i, v)
		}
		energy += float64(v) * float64(v)
	}
	if energy == 0 {
		t.Fatalf("PLC PCM is silent after LACE-armed SILK WB packet")
	}

	t.Run("stereo", func(t *testing.T) {
		dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(stereo 48kHz): %v", err)
		}
		if err := dec.SetComplexity(6); err != nil {
			t.Fatalf("SetComplexity(6): %v", err)
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

		silkWB := makeValidStereoSILKPacketForFrameSizeBandwidthForOSCEBWETest(t, frameSize, BandwidthWideband)
		pcmGood := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
		gotGood, err := dec.Decode(silkWB, pcmGood)
		if err != nil {
			t.Fatalf("Decode(stereo silk WB): %v", err)
		}
		if gotGood != frameSize {
			t.Fatalf("Decode(stereo silk WB) returned %d samples, want %d", gotGood, frameSize)
		}
		if dec.lastPacketMode != ModeSILK || dec.lastBandwidth != BandwidthWideband || !dec.prevPacketStereo {
			t.Fatalf("decoder state after good stereo SILK WB packet: mode=%v bandwidth=%v stereo=%v, want SILK WB stereo", dec.lastPacketMode, dec.lastBandwidth, dec.prevPacketStereo)
		}
		if dec.osceLACE == nil || !dec.osceLACE.prevLACEActive {
			t.Fatalf("prevLACEActive=false after stereo SILK WB decode")
		}

		pcmPLC := make([]float32, frameSize*dec.Channels())
		gotPLC, err := dec.Decode(nil, pcmPLC)
		if err != nil {
			t.Fatalf("Decode(nil) stereo PLC: %v", err)
		}
		if gotPLC != frameSize {
			t.Fatalf("Decode(nil) stereo PLC returned %d samples, want %d", gotPLC, frameSize)
		}
		if dec.osceLACE == nil || dec.osceLACE.laceResetFrames != [2]int{2, 2} || dec.osceLACE.laceMethod != osceLACEModeLACE {
			t.Fatal("stereo SILK WB PLC must retain LACE and arm both channel resets")
		}

		var leftEnergy, rightEnergy, diffEnergy float64
		for i := 0; i < gotPLC; i++ {
			l := pcmPLC[2*i]
			r := pcmPLC[2*i+1]
			if math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) {
				t.Fatalf("stereo PLC left PCM contains NaN/Inf at sample %d: %v", i, l)
			}
			if math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) {
				t.Fatalf("stereo PLC right PCM contains NaN/Inf at sample %d: %v", i, r)
			}
			leftEnergy += float64(l) * float64(l)
			rightEnergy += float64(r) * float64(r)
			diff := float64(l - r)
			diffEnergy += diff * diff
		}
		if leftEnergy == 0 {
			t.Fatalf("stereo PLC left channel is silent after LACE-armed SILK WB packet")
		}
		if rightEnergy == 0 {
			t.Fatalf("stereo PLC right channel is silent after LACE-armed SILK WB packet")
		}
		if diffEnergy == 0 {
			t.Fatalf("stereo PLC collapsed to mono after LACE-armed stereo SILK WB packet")
		}
	})
}
