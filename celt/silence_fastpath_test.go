package celt

import (
	"math"
	"testing"
)

func cloneDecoderStateForSilenceTest(src *Decoder) *Decoder {
	dst := NewDecoder(src.channels)
	dst.sampleRate = src.sampleRate
	dst.bandwidth = src.bandwidth
	dst.rng = src.rng

	dst.postfilterPeriod = src.postfilterPeriod
	dst.postfilterGain = src.postfilterGain
	dst.postfilterTapset = src.postfilterTapset
	dst.postfilterPeriodOld = src.postfilterPeriodOld
	dst.postfilterGainOld = src.postfilterGainOld
	dst.postfilterTapsetOld = src.postfilterTapsetOld

	copy(dst.prevEnergy, src.prevEnergy)
	copy(dst.prevEnergy2, src.prevEnergy2)
	copy(dst.prevLogE, src.prevLogE)
	copy(dst.prevLogE2, src.prevLogE2)
	copy(dst.overlapBuffer, src.overlapBuffer)
	copy(dst.preemphState, src.preemphState)
	copy(dst.postfilterMem, src.postfilterMem)

	return dst
}

func seedDecoderStateForSilenceTest(d *Decoder) {
	for i := range d.overlapBuffer {
		d.overlapBuffer[i] = math.Sin(float64(i+3)*0.17) * 0.12
	}
	for i := range d.preemphState {
		d.preemphState[i] = math.Cos(float64(i+1)*0.73) * 0.07
	}
	for i := range d.postfilterMem {
		d.postfilterMem[i] = math.Sin(float64(i+11)*0.03) * 0.03
	}
	for i := range d.prevEnergy {
		d.prevEnergy[i] = math.Sin(float64(i+1)*0.11) * 2
		d.prevEnergy2[i] = math.Cos(float64(i+1)*0.05) * 2
		d.prevLogE[i] = -20.0 + 0.01*float64(i)
		d.prevLogE2[i] = -22.0 + 0.01*float64(i)
	}
	d.postfilterPeriodOld = 91
	d.postfilterGainOld = 0.33
	d.postfilterTapsetOld = 1
	d.postfilterPeriod = 87
	d.postfilterGain = 0.21
	d.postfilterTapset = 2
}

func decodeSilenceFrameReferenceForTest(d *Decoder, frameSize int, newPeriod int, newGain float64, newTapset int) []float64 {
	mode := GetModeConfig(frameSize)
	zeros := make([]float64, frameSize)
	var samples []float64
	if d.channels == 2 {
		samples = d.SynthesizeStereo(zeros, zeros, false, 1)
	} else {
		samples = d.Synthesize(zeros, false, 1)
	}
	if len(samples) == 0 {
		return nil
	}
	d.applyPostfilter(samples, frameSize, mode.LM, newPeriod, newGain, newTapset)
	d.applyDeemphasisAndScale(samples, 1.0/32768.0)
	out := make([]float64, len(samples))
	copy(out, samples)
	return out
}

func requireFloatSliceClose(t *testing.T, name string, got, want []float64, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length mismatch: got=%d want=%d", name, len(got), len(want))
	}
	for i := range got {
		if math.Abs(got[i]-want[i]) > tol {
			t.Fatalf("%s mismatch at %d: got=%v want=%v", name, i, got[i], want[i])
		}
	}
}

func TestDecodeSilenceFrameFastPathParityMono(t *testing.T) {
	const frameSize = 960
	base := NewDecoder(1)
	seedDecoderStateForSilenceTest(base)

	ref := cloneDecoderStateForSilenceTest(base)
	gotDec := cloneDecoderStateForSilenceTest(base)

	const (
		newPeriod = 77
		newGain   = 0.27
		newTapset = 1
	)
	want := decodeSilenceFrameReferenceForTest(ref, frameSize, newPeriod, newGain, newTapset)
	got := gotDec.decodeSilenceFrame(frameSize, newPeriod, newGain, newTapset)

	requireFloatSliceClose(t, "samples", got, want, 1e-7)
	requireFloatSliceClose(t, "overlap", gotDec.overlapBuffer, ref.overlapBuffer, 1e-7)
	requireFloatSliceClose(t, "preemph", gotDec.preemphState, ref.preemphState, 1e-9)
	requireFloatSliceClose(t, "postfilterMem", gotDec.postfilterMem, ref.postfilterMem, 1e-9)
	if gotDec.postfilterPeriod != ref.postfilterPeriod ||
		gotDec.postfilterPeriodOld != ref.postfilterPeriodOld ||
		gotDec.postfilterTapset != ref.postfilterTapset ||
		gotDec.postfilterTapsetOld != ref.postfilterTapsetOld ||
		math.Abs(gotDec.postfilterGain-ref.postfilterGain) > 1e-12 ||
		math.Abs(gotDec.postfilterGainOld-ref.postfilterGainOld) > 1e-12 {
		t.Fatalf("postfilter state mismatch")
	}
}

func TestDecodeSilenceFrameFastPathParityStereo(t *testing.T) {
	const frameSize = 960
	base := NewDecoder(2)
	seedDecoderStateForSilenceTest(base)

	ref := cloneDecoderStateForSilenceTest(base)
	gotDec := cloneDecoderStateForSilenceTest(base)

	const (
		newPeriod = 83
		newGain   = 0.31
		newTapset = 2
	)
	want := decodeSilenceFrameReferenceForTest(ref, frameSize, newPeriod, newGain, newTapset)
	got := gotDec.decodeSilenceFrame(frameSize, newPeriod, newGain, newTapset)

	requireFloatSliceClose(t, "samples", got, want, 1e-7)
	requireFloatSliceClose(t, "overlap", gotDec.overlapBuffer, ref.overlapBuffer, 1e-7)
	requireFloatSliceClose(t, "preemph", gotDec.preemphState, ref.preemphState, 1e-9)
	requireFloatSliceClose(t, "postfilterMem", gotDec.postfilterMem, ref.postfilterMem, 1e-9)
	if gotDec.postfilterPeriod != ref.postfilterPeriod ||
		gotDec.postfilterPeriodOld != ref.postfilterPeriodOld ||
		gotDec.postfilterTapset != ref.postfilterTapset ||
		gotDec.postfilterTapsetOld != ref.postfilterTapsetOld ||
		math.Abs(gotDec.postfilterGain-ref.postfilterGain) > 1e-12 ||
		math.Abs(gotDec.postfilterGainOld-ref.postfilterGainOld) > 1e-12 {
		t.Fatalf("postfilter state mismatch")
	}
}
