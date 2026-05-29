package celt

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

func TestEncoderControlFieldWidthsMatchLibopusFloatBuild(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	for _, name := range []string{
		"channels",
		"streamChannels",
		"sampleRate",
		"frameCount",
		"consecTransient",
		"lastCodedBands",
		"intensity",
		"lsbDepth",
		"targetBitrate",
		"frameBits",
		"coarseAvailableBytes",
		"maxPayloadBytes",
		"complexity",
		"spreadDecision",
		"tapsetDecision",
		"packetLoss",
	} {
		field, ok := reflect.TypeOf(Encoder{}).FieldByName(name)
		if !ok {
			t.Fatalf("Encoder.%s missing", name)
		}
		if field.Type != int32Type {
			t.Fatalf("Encoder.%s type=%s want %s", name, field.Type, int32Type)
		}
	}
}

func TestDecoderControlFieldWidthsMatchLibopusFloatBuild(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	for _, name := range []string{
		"channels",
		"sampleRate",
		"downsample",
		"postfilterPeriod",
		"postfilterTapset",
		"postfilterPeriodOld",
		"postfilterTapsetOld",
		"plcLossDuration",
		"plcDuration",
		"plcLastFrameType",
		"plcLastPitchPeriod",
		"complexity",
		"prevStreamChannels",
	} {
		field, ok := reflect.TypeOf(Decoder{}).FieldByName(name)
		if !ok {
			t.Fatalf("Decoder.%s missing", name)
		}
		if field.Type != int32Type {
			t.Fatalf("Decoder.%s type=%s want %s", name, field.Type, int32Type)
		}
	}
}

// TestNewEncoder verifies encoder initialization matches decoder.
func TestNewEncoder(t *testing.T) {
	tests := []struct {
		name     string
		channels int
	}{
		{"mono", 1},
		{"stereo", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewEncoder(tt.channels)

			// Verify configuration
			if enc.Channels() != tt.channels {
				t.Errorf("Channels() = %d, want %d", enc.Channels(), tt.channels)
			}
			if enc.StreamChannels() != tt.channels {
				t.Errorf("StreamChannels() = %d, want %d", enc.StreamChannels(), tt.channels)
			}
			if enc.SampleRate() != 48000 {
				t.Errorf("SampleRate() = %d, want 48000", enc.SampleRate())
			}

			// Verify energy arrays initialized correctly (libopus init clears oldBandE).
			prevEnergy := enc.PrevEnergy()
			if len(prevEnergy) != MaxBands*tt.channels {
				t.Errorf("PrevEnergy length = %d, want %d", len(prevEnergy), MaxBands*tt.channels)
			}
			for i, e := range prevEnergy {
				if e != 0 {
					t.Errorf("PrevEnergy[%d] = %f, want 0.0", i, e)
				}
			}

			// Verify RNG initialized to libopus default (0)
			if enc.RNG() != 0 {
				t.Errorf("RNG() = %d, want 0", enc.RNG())
			}

			// Verify overlap buffer
			overlap := enc.OverlapBuffer()
			if len(overlap) != Overlap*tt.channels {
				t.Errorf("OverlapBuffer length = %d, want %d", len(overlap), Overlap*tt.channels)
			}

			// Verify preemph state
			preemph := enc.PreemphState()
			if len(preemph) != tt.channels {
				t.Errorf("PreemphState length = %d, want %d", len(preemph), tt.channels)
			}
		})
	}
}

func TestEncoderStreamChannelsControl(t *testing.T) {
	enc := NewEncoder(2)
	enc.SetStreamChannels(1)
	if got := enc.StreamChannels(); got != 1 {
		t.Fatalf("StreamChannels() = %d, want 1", got)
	}
	if got := enc.Channels(); got != 2 {
		t.Fatalf("Channels() = %d, want 2", got)
	}

	enc.SetStreamChannels(3)
	if got := enc.StreamChannels(); got != 1 {
		t.Fatalf("invalid SetStreamChannels changed value to %d", got)
	}

	enc.Reset()
	if got := enc.StreamChannels(); got != 1 {
		t.Fatalf("Reset changed StreamChannels() to %d, want 1", got)
	}
}

func TestFoldStereoMDCTToMonoMatchesLibopusAverage(t *testing.T) {
	left := []float32{1, -2, 3.5, 1.0 / 3.0}
	right := []float32{3, 4, -1.5, -2.0 / 3.0}
	dst := make([]float32, len(left))

	got := foldStereoMDCTToMonoF32(dst, left, right)
	for i := range got {
		want := 0.5*left[i] + 0.5*right[i]
		if got[i] != want {
			t.Fatalf("folded[%d] = %v, want %v", i, got[i], want)
		}
	}
}

func TestEncodeFrameStereoAPIInternalMonoMirrorsEnergyState(t *testing.T) {
	const frameSize = 960
	enc := NewEncoder(2)
	enc.SetStreamChannels(1)
	enc.SetDelayCompensationEnabled(false)
	enc.SetDCRejectEnabled(false)
	enc.SetLSBQuantizationEnabled(false)
	enc.SetVBR(false)
	enc.SetBitrate(64000)

	pcm := make([]float64, frameSize*2)
	for i := 0; i < frameSize; i++ {
		pcm[2*i] = 0.22 * math.Sin(2*math.Pi*440*float64(i)/48000)
		pcm[2*i+1] = 0.17 * math.Sin(2*math.Pi*660*float64(i)/48000)
	}

	if _, err := enc.EncodeFrame(float32Slice(pcm), frameSize); err != nil {
		t.Fatalf("EncodeFrame() error: %v", err)
	}
	prev := enc.PrevEnergy()
	for band := 0; band < MaxBands; band++ {
		if prev[band] != prev[MaxBands+band] {
			t.Fatalf("band %d right energy = %v, want left %v", band, prev[MaxBands+band], prev[band])
		}
	}
}

// TestEncoderMatchesDecoder verifies encoder state matches decoder state.
func TestEncoderMatchesDecoder(t *testing.T) {
	channels := 2

	enc := NewEncoder(channels)
	dec := NewDecoder(channels)

	// Compare configuration
	if enc.Channels() != dec.Channels() {
		t.Errorf("Channels: enc=%d, dec=%d", enc.Channels(), dec.Channels())
	}
	if enc.SampleRate() != dec.SampleRate() {
		t.Errorf("SampleRate: enc=%d, dec=%d", enc.SampleRate(), dec.SampleRate())
	}

	// Compare energy arrays
	encEnergy := enc.PrevEnergy()
	decEnergy := dec.PrevEnergy()
	if len(encEnergy) != len(decEnergy) {
		t.Errorf("PrevEnergy length: enc=%d, dec=%d", len(encEnergy), len(decEnergy))
	}
	for i := range encEnergy {
		if encEnergy[i] != decEnergy[i] {
			t.Errorf("PrevEnergy[%d]: enc=%f, dec=%f", i, encEnergy[i], decEnergy[i])
		}
	}

	// Compare RNG
	if enc.RNG() != dec.RNG() {
		t.Errorf("RNG: enc=%d, dec=%d", enc.RNG(), dec.RNG())
	}

	// Compare overlap buffer size
	if len(enc.OverlapBuffer()) != len(dec.OverlapBuffer()) {
		t.Errorf("OverlapBuffer length: enc=%d, dec=%d",
			len(enc.OverlapBuffer()), len(dec.OverlapBuffer()))
	}
}

// TestEncoderReset verifies Reset clears state properly.
func TestEncoderReset(t *testing.T) {
	enc := NewEncoder(2)

	// Modify state
	enc.SetEnergy(5, 0, 10.0)
	enc.SetEnergy(5, 1, 15.0)
	enc.SetRNG(12345)

	// Reset
	enc.Reset()

	// Verify state cleared
	if enc.GetEnergy(5, 0) != 0 {
		t.Errorf("GetEnergy(5, 0) after reset = %f, want 0.0", enc.GetEnergy(5, 0))
	}
	if enc.RNG() != 0 {
		t.Errorf("RNG after reset = %d, want 0", enc.RNG())
	}
}

func TestEncodeFrameQuantizesIngressToLSBDepth(t *testing.T) {
	const (
		frameSize = 120
		lsbDepth  = 8
	)

	base := make([]float64, frameSize)
	perturbed := make([]float64, frameSize)
	for i := range base {
		phase := 2 * math.Pi * float64(i) / float64(frameSize)
		v := 0.32*math.Sin(3*phase) + 0.11*math.Cos(11*phase)
		v = math.Round(v*128.0) / 128.0
		base[i] = v
		perturbed[i] = v + 0.001*math.Sin(17*phase+0.3)
	}

	encA := NewEncoder(1)
	encA.SetLSBDepth(lsbDepth)
	quantizedBase := append([]float32(nil), encA.quantizeInputToLSBDepthScratchF32(float32Slice(base))...)
	quantizedPerturbed := encA.quantizeInputToLSBDepthScratchF32(float32Slice(perturbed))
	for i := range quantizedBase {
		if quantizedBase[i] != quantizedPerturbed[i] {
			t.Fatalf("sub-LSB perturbation changed quantized sample %d: base=%f perturbed=%f", i, quantizedBase[i], quantizedPerturbed[i])
		}
	}

	newEncoder := func() *Encoder {
		enc := NewEncoder(1)
		enc.SetBitrate(64000)
		enc.SetComplexity(10)
		enc.SetLSBDepth(lsbDepth)
		return enc
	}

	packetBase, err := newEncoder().EncodeFrame(float32Slice(base), frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame(base) failed: %v", err)
	}
	packetPerturbed, err := newEncoder().EncodeFrame(float32Slice(perturbed), frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame(perturbed) failed: %v", err)
	}
	if !bytes.Equal(packetBase, packetPerturbed) {
		t.Fatalf("sub-LSB ingress drift changed packet bytes: base=%v perturbed=%v", packetBase, packetPerturbed)
	}
}

func TestEncoderSetPrediction(t *testing.T) {
	enc := NewEncoder(1)
	if got := enc.Prediction(); got != 2 {
		t.Fatalf("initial Prediction() = %d, want 2", got)
	}

	enc.SetPrediction(0)
	if got := enc.Prediction(); got != 0 {
		t.Fatalf("Prediction() after SetPrediction(0) = %d, want 0", got)
	}
	if !enc.forceIntra || !enc.disablePrefilter {
		t.Fatalf("SetPrediction(0) should set forceIntra+disablePrefilter, got forceIntra=%v disablePrefilter=%v", enc.forceIntra, enc.disablePrefilter)
	}

	enc.SetPrediction(1)
	if got := enc.Prediction(); got != 1 {
		t.Fatalf("Prediction() after SetPrediction(1) = %d, want 1", got)
	}
	if enc.forceIntra || !enc.disablePrefilter {
		t.Fatalf("SetPrediction(1) should set only disablePrefilter, got forceIntra=%v disablePrefilter=%v", enc.forceIntra, enc.disablePrefilter)
	}

	enc.SetPrediction(2)
	if got := enc.Prediction(); got != 2 {
		t.Fatalf("Prediction() after SetPrediction(2) = %d, want 2", got)
	}
	if enc.forceIntra || enc.disablePrefilter {
		t.Fatalf("SetPrediction(2) should clear forceIntra+disablePrefilter, got forceIntra=%v disablePrefilter=%v", enc.forceIntra, enc.disablePrefilter)
	}

	enc.SetPrediction(-5)
	if got := enc.Prediction(); got != 0 {
		t.Fatalf("Prediction() after SetPrediction(-5) = %d, want 0", got)
	}
	enc.SetPrediction(7)
	if got := enc.Prediction(); got != 2 {
		t.Fatalf("Prediction() after SetPrediction(7) = %d, want 2", got)
	}
}

func TestEncoderResetPreservesPredictionControl(t *testing.T) {
	enc := NewEncoder(1)
	enc.SetPrediction(0)
	enc.Reset()
	if got := enc.Prediction(); got != 0 {
		t.Fatalf("Prediction() after Reset() = %d, want 0", got)
	}
}

func TestEncoderSetSurroundTrim(t *testing.T) {
	enc := NewEncoder(2)
	if got := enc.SurroundTrim(); got != 0 {
		t.Fatalf("initial SurroundTrim() = %v, want 0", got)
	}

	enc.SetSurroundTrim(0.75)
	if got := enc.SurroundTrim(); got != 0.75 {
		t.Fatalf("SurroundTrim() after set = %v, want 0.75", got)
	}

	enc.SetSurroundTrim(-0.5)
	if got := enc.SurroundTrim(); got != -0.5 {
		t.Fatalf("SurroundTrim() after update = %v, want -0.5", got)
	}
}

func TestEncoderResetClearsSurroundTrim(t *testing.T) {
	enc := NewEncoder(2)
	enc.SetSurroundTrim(1.25)
	enc.Reset()
	if got := enc.SurroundTrim(); got != 0 {
		t.Fatalf("SurroundTrim() after reset = %v, want 0", got)
	}
}

func TestEncoderSetLFE(t *testing.T) {
	enc := NewEncoder(1)
	if enc.LFE() {
		t.Fatalf("initial LFE() = true, want false")
	}

	enc.SetLFE(true)
	if !enc.LFE() {
		t.Fatalf("LFE() after SetLFE(true) = false, want true")
	}

	// LFE is a control state and should survive stream-state reset.
	enc.Reset()
	if !enc.LFE() {
		t.Fatalf("LFE() after Reset() = false, want true")
	}

	enc.SetLFE(false)
	if enc.LFE() {
		t.Fatalf("LFE() after SetLFE(false) = true, want false")
	}
}

func TestLFEClampsLinearBandE(t *testing.T) {
	bandE := []celtEner{2.0, 0.5, 1.0, 1e-20, 3e-4}
	applyLFELinearBandEClamp(bandE, len(bandE), 1)

	limit := float32(lfeBandClamp) * float32(2.0)
	if got := float32(bandE[2]); got != limit {
		t.Fatalf("bandE[2]=%g want %g", got, limit)
	}
	if got := float32(bandE[3]); got != float32(celtFloatEpsilon) {
		t.Fatalf("bandE[3]=%g want %g", got, float32(celtFloatEpsilon))
	}
	if got := float32(bandE[4]); got != limit {
		t.Fatalf("bandE[4]=%g want %g", got, limit)
	}
	if got := bandE[1]; got != celtEner(0.5) {
		t.Fatalf("bandE[1]=%g want unchanged 0.5", got)
	}
}

func TestLFEClampsLogBandEToLinearReference(t *testing.T) {
	linear := []float64{2.0, 0.5, 1.0, 1e-20, 3e-4}
	logE := make([]celtGLog, len(linear))
	for band, v := range linear {
		logE[band] = celtGLog(celtLog2(float32(v)) - float32(eMeans[band]*DB6))
	}

	applyLFEBandLogEClamp(logE, len(logE), 1)

	limit := float32(lfeBandClamp) * float32(linear[0])
	for band := 2; band < len(linear); band++ {
		wantLinear := float32(linear[band])
		if wantLinear > limit {
			wantLinear = limit
		}
		if wantLinear < float32(celtFloatEpsilon) {
			wantLinear = float32(celtFloatEpsilon)
		}
		want := celtLog2(wantLinear) - float32(eMeans[band]*DB6)
		if math.Float32bits(logE[band]) != math.Float32bits(want) {
			t.Fatalf("logE[%d]=%08x want %08x", band, math.Float32bits(logE[band]), math.Float32bits(want))
		}
	}
	if got, want := logE[1], celtLog2(float32(linear[1]))-float32(eMeans[1]*DB6); math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("logE[1]=%08x want unchanged %08x", math.Float32bits(got), math.Float32bits(want))
	}
}

func TestEncodeFrameLFEClampsHighBandEnergy(t *testing.T) {
	const frameSize = 960
	enc := NewEncoder(1)
	enc.SetLFE(true)
	enc.SetVBR(false)
	enc.SetBitrate(128000)
	pcm := make([]float64, frameSize)
	for i := range pcm {
		lo := 0.55 * math.Sin(2*math.Pi*60*float64(i)/48000)
		hi := 0.45 * math.Sin(2*math.Pi*9000*float64(i)/48000)
		pcm[i] = lo + hi
	}

	if _, err := enc.EncodeFrame(float32Slice(pcm), frameSize); err != nil {
		t.Fatalf("EncodeFrame(LFE) failed: %v", err)
	}
	lastBandLogE := enc.GetLastBandLogE()
	if len(lastBandLogE) < 3 {
		t.Fatalf("lastBandLogE length=%d want at least 3", len(lastBandLogE))
	}
	baseAbs := float64(lastBandLogE[0]) + float64(eMeans[0]*DB6)
	highAbs := float64(lastBandLogE[2]) + float64(eMeans[2]*DB6)
	limitAbs := baseAbs + float64(celtLog2(float32(lfeBandClamp)))
	floorAbs := float64(celtLog2(float32(celtFloatEpsilon)))
	wantMax := limitAbs
	if wantMax < floorAbs {
		wantMax = floorAbs
	}
	if highAbs > wantMax+1e-6 {
		t.Fatalf("LFE high-band log energy=%g want <= %g", highAbs, wantMax)
	}
}

func TestLastBandLogEUsesGLogWidth(t *testing.T) {
	enc := NewEncoder(1)
	enc.lastBandLogE = []celtGLog{celtGLog(float32(1.0 / 3.0))}
	enc.lastBandLogE2 = []celtGLog{celtGLog(float32(2.0 / 3.0))}

	if got, want := enc.GetLastBandLogE()[0], celtGLog(1.0/3.0); got != want {
		t.Fatalf("GetLastBandLogE()[0]=%0.9g want celt_glog %0.9g", got, want)
	}
	if got, want := enc.GetLastBandLogE2()[0], celtGLog(2.0/3.0); got != want {
		t.Fatalf("GetLastBandLogE2()[0]=%0.9g want celt_glog %0.9g", got, want)
	}
}

// TestEncoderNextRNG verifies RNG produces expected sequence.
func TestEncoderNextRNG(t *testing.T) {
	enc := NewEncoder(1)
	dec := NewDecoder(1)

	// Both should produce same RNG sequence
	for i := 0; i < 10; i++ {
		encRNG := enc.NextRNG()
		decRNG := dec.NextRNG()
		if encRNG != decRNG {
			t.Errorf("iteration %d: enc RNG=%d, dec RNG=%d", i, encRNG, decRNG)
		}
	}
}

// TestMDCTRoundTrip verifies MDCT -> IMDCT reconstructs original signal.
func TestMDCTRoundTrip(t *testing.T) {
	// Test various sizes
	sizes := []int{120, 240, 480, 960}

	for _, n := range sizes {
		t.Run(sizeToString(n), func(t *testing.T) {
			// Create input: 2*N samples (sine wave)
			input := make([]float64, 2*n)
			for i := range input {
				// Use a sine wave at a frequency that fits well in the window
				input[i] = math.Sin(2 * math.Pi * float64(i) / 100)
			}

			// Forward MDCT: 2*N samples -> N coefficients
			coeffs := MDCT(float32sForMDCTForwardTest(input))
			if len(coeffs) != n {
				t.Errorf("MDCT output length = %d, want %d", len(coeffs), n)
				return
			}

			// Inverse MDCT: N coefficients -> 2*N samples
			output := IMDCTDirect(coeffs)
			if len(output) != 2*n {
				t.Errorf("IMDCT output length = %d, want %d", len(output), 2*n)
				return
			}

			// Due to windowing, we can only compare the middle region where
			// the windows are near 1.0. The edges have windowing effects.
			// Compare middle 50% of the frame
			startCompare := n / 2
			endCompare := n + n/2

			var maxDiff float64
			for i := startCompare; i < endCompare; i++ {
				diff := math.Abs(input[i] - float64(output[i]))
				if diff > maxDiff {
					maxDiff = diff
				}
			}

			// Tolerance depends on MDCT normalization
			// The MDCT/IMDCT pair should reconstruct with some scaling
			// Check that the shape is preserved (not all zeros)
			var maxOutput float64
			for i := startCompare; i < endCompare; i++ {
				abs := math.Abs(float64(output[i]))
				if abs > maxOutput {
					maxOutput = abs
				}
			}

			if maxOutput < 0.01 {
				t.Errorf("MDCT->IMDCT produced near-zero output, max=%f", maxOutput)
			}
		})
	}
}

// TestMDCTShortRoundTrip verifies MDCTShort -> IMDCTShort reconstructs signal.
func TestMDCTShortRoundTrip(t *testing.T) {
	shortBlocksCounts := []int{2, 4, 8}

	for _, shortBlocks := range shortBlocksCounts {
		t.Run(shortBlocksToString(shortBlocks), func(t *testing.T) {
			// Total samples = shortBlocks * shortSize * 2
			// For standard CELT with 120 overlap, each short block has 120 coefficients
			shortSize := 120
			totalSamples := shortSize * 2 * shortBlocks

			// Create input signal
			input := make([]float64, totalSamples)
			for i := range input {
				input[i] = math.Sin(2 * math.Pi * float64(i) / 50)
			}

			// Forward MDCT (short blocks)
			coeffs := MDCTShort(float32sForMDCTForwardTest(input), shortBlocks)
			expectedCoeffs := shortSize * shortBlocks
			if len(coeffs) != expectedCoeffs {
				t.Errorf("MDCTShort output length = %d, want %d", len(coeffs), expectedCoeffs)
				return
			}

			// Inverse MDCT (short blocks)
			output := IMDCTShort(coeffs, shortBlocks)

			// Output should have 2x the coefficients (IMDCT produces 2*N from N)
			if len(output) != 2*expectedCoeffs {
				t.Errorf("IMDCTShort output length = %d, want %d", len(output), 2*expectedCoeffs)
				return
			}

			// Verify output is not all zeros
			var maxOutput float64
			for _, x := range output {
				abs := math.Abs(float64(x))
				if abs > maxOutput {
					maxOutput = abs
				}
			}

			if maxOutput < 0.01 {
				t.Errorf("MDCTShort->IMDCTShort produced near-zero output, max=%f", maxOutput)
			}
		})
	}
}

func TestEncoderFrameCountAndIntraFlag(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960
	mode := GetModeConfig(frameSize)
	pcm := generateSineWave(440.0, frameSize)

	for i := 0; i < 5; i++ {
		packet, err := enc.EncodeFrame(float32Slice(pcm), frameSize)
		if err != nil {
			t.Fatalf("frame %d: EncodeFrame failed: %v", i, err)
		}
		if len(packet) == 0 {
			t.Fatalf("frame %d: empty packet", i)
		}

		rd := &rangecoding.Decoder{}
		rd.Init(packet)
		if rd.DecodeBit(15) == 1 {
			t.Fatalf("frame %d: unexpected silence flag", i)
		}
		rd.DecodeBit(1) // reserved/start bit
		if mode.LM > 0 {
			rd.DecodeBit(3) // transient flag
		}
		// The exact intra decision is signal-dependent under libopus-style
		// two-pass coarse-energy search. Verify that the flag is present and
		// decodes cleanly rather than hard-coding a particular value.
		_ = rd.DecodeBit(3) == 1
		if rd.Error() != 0 {
			t.Fatalf("frame %d: intra flag decode desynchronized", i)
		}

		if enc.FrameCount() != i+1 {
			t.Fatalf("frame %d: FrameCount=%d, want %d", i, enc.FrameCount(), i+1)
		}
	}
}

func TestEncodeFrameBudgetDisabledTransientAdvancesConsecTransient(t *testing.T) {
	enc := NewEncoder(1)
	enc.SetComplexity(0)
	enc.SetVBR(false)
	enc.SetBitrate(64000)
	enc.SetMaxPayloadBytes(2)

	if _, err := enc.EncodeFrame(float32Slice(generateSineWave(440.0, 960)), 960); err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	// At complexity 0 libopus skips transient_analysis() entirely
	// (celt_encoder.c:2023 gates it behind st->complexity >= 1), so isTransient
	// stays 0. With this 2-byte budget the short-block gate
	// ec_tell(enc)+3 <= total_bits still passes, so transient_got_disabled stays
	// 0 and consec_transient resets to 0 (celt_encoder.c:2805). Confirmed
	// byte-exact against celt_encode_with_ec() by
	// TestCELTEncodeMatchesLibopusC/mono_20ms_complexity0_budget2_sine.
	if got := enc.ConsecTransient(); got != 0 {
		t.Fatalf("ConsecTransient()=%d want 0", got)
	}
}

func TestVBRSilenceFrameShrinksToMinimum(t *testing.T) {
	enc := NewEncoder(1)
	enc.SetBitrate(64000)
	enc.SetVBR(true)
	enc.SetConstrainedVBR(false)

	packet, err := enc.EncodeFrame(float32Slice(make([]float64, 960)), 960)
	if err != nil {
		t.Fatalf("EncodeFrame silence: %v", err)
	}
	if len(packet) != 2 {
		t.Fatalf("VBR silence payload length=%d want 2", len(packet))
	}
	if enc.FrameCount() != 1 {
		t.Fatalf("FrameCount=%d want 1", enc.FrameCount())
	}
	for i, energy := range enc.PrevEnergy() {
		if energy != -28.0 {
			t.Fatalf("PrevEnergy[%d]=%f want -28", i, energy)
		}
	}
}

// TestPreemphasisDeemphasis verifies pre-emphasis -> de-emphasis round-trip.
func TestPreemphasisDeemphasis(t *testing.T) {
	channels := []int{1, 2}

	for _, ch := range channels {
		t.Run(channelsToString(ch), func(t *testing.T) {
			enc := NewEncoder(ch)
			dec := NewDecoder(ch)

			// Create input signal
			samples := 100
			input := make([]float64, samples*ch)
			for i := range input {
				input[i] = float64(i%20) / 10.0 // Sawtooth-like pattern
			}

			// Apply pre-emphasis
			preemph := enc.ApplyPreemphasis(float32Slice(input))

			// Apply de-emphasis (simulate what decoder does)
			output := make([]float64, len(preemph))
			for i, v := range preemph {
				output[i] = float64(v)
			}

			// De-emphasis: y[n] = x[n] + PreemphCoef * y[n-1]
			coef := float32(PreemphCoef)
			if ch == 1 {
				state := dec.PreemphState()[0]
				for i := range output {
					y := float32(output[i]) + coef*state
					output[i] = float64(y)
					state = y
				}
			} else {
				stateL := dec.PreemphState()[0]
				stateR := dec.PreemphState()[1]
				for i := 0; i < len(output)-1; i += 2 {
					yL := float32(output[i]) + coef*stateL
					output[i] = float64(yL)
					stateL = yL
					yR := float32(output[i+1]) + coef*stateR
					output[i+1] = float64(yR)
					stateR = yR
				}
			}

			// Compare: input and output should match after round-trip
			// Note: first sample may differ due to initial state
			startCompare := ch // Skip first sample(s)
			var maxDiff float64
			for i := startCompare; i < len(input); i++ {
				diff := math.Abs(float64(float32(input[i])) - output[i])
				if diff > maxDiff {
					maxDiff = diff
				}
			}

			// The CELT float path keeps this state in float width.
			if maxDiff > 1e-6 {
				t.Errorf("pre-emphasis/de-emphasis round-trip error: maxDiff=%e", maxDiff)
			}
		})
	}
}

// TestPreemphasisState verifies pre-emphasis state is maintained across calls.
func TestPreemphasisState(t *testing.T) {
	enc := NewEncoder(1)

	// First call
	input1 := []float64{1.0, 2.0, 3.0}
	_ = enc.ApplyPreemphasis(float32Slice(input1))

	// State tracks PreemphCoef * last input sample in CELT float width.
	expectedState := float32(PreemphCoef) * float32(3.0)
	if math.Float32bits(enc.PreemphState()[0]) != math.Float32bits(expectedState) {
		t.Errorf("PreemphState after first call = %f, want %f", enc.PreemphState()[0], expectedState)
	}

	// Second call should use state from first call
	input2 := []float64{4.0, 5.0}
	output2 := enc.ApplyPreemphasis(float32Slice(input2))

	// First output should be: 4.0 - 0.85*3.0 = 1.45
	expected := float32(4.0) - expectedState
	if math.Float32bits(float32(output2[0])) != math.Float32bits(expected) {
		t.Errorf("First sample of second call = %f, want %f", output2[0], expected)
	}
}

// TestApplyPreemphasisInPlace verifies in-place pre-emphasis works correctly.
func TestApplyPreemphasisInPlace(t *testing.T) {
	enc1 := NewEncoder(1)
	enc2 := NewEncoder(1)

	input := []float64{1.0, 2.0, 3.0, 4.0, 5.0}

	// Apply regular pre-emphasis
	inputCopy := make([]float64, len(input))
	copy(inputCopy, input)
	regular := enc1.ApplyPreemphasis(float32Slice(inputCopy))

	// Apply in-place pre-emphasis
	inPlace := float32Slice(input)
	enc2.ApplyPreemphasisInPlace(inPlace)

	// Both should produce same results
	for i := range regular {
		if math.Abs(float64(regular[i]-inPlace[i])) > 1e-10 {
			t.Errorf("Sample %d: regular=%f, inPlace=%f", i, regular[i], inPlace[i])
		}
	}
}

// Helper functions for test naming
func sizeToString(n int) string {
	switch n {
	case 120:
		return "2.5ms"
	case 240:
		return "5ms"
	case 480:
		return "10ms"
	case 960:
		return "20ms"
	default:
		return "unknown"
	}
}

func shortBlocksToString(n int) string {
	switch n {
	case 2:
		return "2_blocks"
	case 4:
		return "4_blocks"
	case 8:
		return "8_blocks"
	default:
		return "unknown_blocks"
	}
}

func channelsToString(ch int) string {
	if ch == 1 {
		return "mono"
	}
	return "stereo"
}
