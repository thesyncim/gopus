//go:build gopus_extra_controls
// +build gopus_extra_controls

package lpcnetplc

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func TestLPCNetSingleFrameFeaturesFloatMatchesLibopusColdStart(t *testing.T) {
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		t.Skipf("pitchdnn model blob helper unavailable: %v", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone(real pitchdnn blob) error: %v", err)
	}

	var analysis Analysis
	if err := analysis.SetModel(blob); err != nil {
		t.Fatalf("Analysis.SetModel(real model) error: %v", err)
	}

	var frame [FrameSize]float32
	for i := range frame {
		frame[i] = float32((i%43)-21) / 17
	}
	want, err := probeLibopusLPCNetFeatures(frame[:])
	if err != nil {
		t.Skipf("lpcnet features helper unavailable: %v", err)
	}

	var got [NumTotalFeatures]float32
	if n := analysis.ComputeSingleFrameFeaturesFloat(got[:], frame[:]); n != NumTotalFeatures {
		t.Fatalf("ComputeSingleFrameFeaturesFloat()=%d want %d", n, NumTotalFeatures)
	}
	assertFloat32Close(t, got[:], want.Features[:NumTotalFeatures], 5e-3, "cold start features copy-out")
	assertAnalysisMatches(t, &analysis, want, "cold start")
}

func TestLPCNetSingleFrameFeaturesFloatMatchesLibopusStatefulSequence(t *testing.T) {
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		t.Skipf("pitchdnn model blob helper unavailable: %v", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone(real pitchdnn blob) error: %v", err)
	}

	var analysis Analysis
	if err := analysis.SetModel(blob); err != nil {
		t.Fatalf("Analysis.SetModel(real model) error: %v", err)
	}

	frames := make([]float32, 3*FrameSize)
	for i := range frames {
		frames[i] = float32((i%53)-26) / 19
	}
	want, err := probeLibopusLPCNetFeatures(frames)
	if err != nil {
		t.Skipf("lpcnet features helper unavailable: %v", err)
	}
	for i := 0; i < len(frames); i += FrameSize {
		var got [NumTotalFeatures]float32
		if n := analysis.ComputeSingleFrameFeaturesFloat(got[:], frames[i:i+FrameSize]); n != NumTotalFeatures {
			t.Fatalf("frame %d ComputeSingleFrameFeaturesFloat()=%d want %d", i/FrameSize, n, NumTotalFeatures)
		}
		base := (i / FrameSize) * NumTotalFeatures
		assertFloat32Close(t, got[:], want.Features[base:base+NumTotalFeatures], 5e-2, "sequence features")
	}
	assertAnalysisMatches(t, &analysis, want, "stateful sequence")
}

func TestLPCNetDREDSequenceMatchesLibopusTightly(t *testing.T) {
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		t.Skipf("pitchdnn model blob helper unavailable: %v", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone(real pitchdnn blob) error: %v", err)
	}
	var analysis Analysis
	analysis.SetDREDEncoderMode(true)
	if err := analysis.SetModel(blob); err != nil {
		t.Fatalf("Analysis.SetModel(real model) error: %v", err)
	}

	for _, frameSize := range []int{1920, 2880} {
		t.Run(fmt.Sprintf("%d_samples", frameSize), func(t *testing.T) {
			analysis.Reset()
			if err := analysis.SetModel(blob); err != nil {
				t.Fatalf("Analysis.SetModel(real model) error: %v", err)
			}
			frames := dredParityAnalysisFrames(4, frameSize)
			want, err := probeLibopusLPCNetFeatures(frames)
			if err != nil {
				t.Skipf("lpcnet features helper unavailable: %v", err)
			}
			for frame := 0; frame < len(frames)/FrameSize; frame++ {
				var got [NumTotalFeatures]float32
				if n := analysis.ComputeSingleFrameFeaturesFloat(got[:], frames[frame*FrameSize:(frame+1)*FrameSize]); n != NumTotalFeatures {
					t.Fatalf("frame %d ComputeSingleFrameFeaturesFloat()=%d want %d", frame, n, NumTotalFeatures)
				}
				base := frame * NumTotalFeatures
				assertFloat32Close(t, got[:], want.Features[base:base+NumTotalFeatures], 1e-5, "dred sequence features")
			}
			assertFloat32Close(t, analysis.xcorrFeatures[:], want.XCorr, 1e-6, "dred sequence xcorr")
		})
	}
}

func assertAnalysisMatches(t *testing.T, got *Analysis, want libopusLPCNetFeaturesResult, label string) {
	t.Helper()
	assertFloat32Close(t, got.analysisMem[:], want.AnalysisMem, 5e-3, label+" analysis_mem")
	assertFloat32Close(t, []float32{got.memPreemph}, []float32{want.MemPreemph}, 5e-3, label+" mem_preemph")
	assertComplex64Close(t, got.prevIF[:], want.PrevIF, 5e-3, label+" prev_if")
	assertFloat32Close(t, got.ifFeatures[:], want.IFFeatures, 5e-3, label+" if_features")
	assertFloat32Close(t, got.lpc[:], want.LPC, 5e-2, label+" lpc")
	assertFloat32Close(t, got.pitchMem[:], want.PitchMem, 5e-3, label+" pitch_mem")
	assertFloat32Close(t, []float32{got.pitchFilt}, []float32{want.PitchFilt}, 5e-3, label+" pitch_filt")
	assertFloat32Close(t, got.excBuf[:], want.ExcBuf, 5e-2, label+" exc_buf")
	assertFloat32Close(t, got.lpBuf[:], want.LPBuf, 5e-2, label+" lp_buf")
	assertFloat32Close(t, got.lpMem[:], want.LPMem, 5e-2, label+" lp_mem")
	assertFloat32Close(t, got.xcorrFeatures[:], want.XCorr, 5e-2, label+" xcorr_features")
	assertFloat32Close(t, []float32{got.dnnPitch}, []float32{want.DNNPitch}, 5e-3, label+" dnn_pitch")
	assertFloat32Close(t, got.pitch.state.gruState[:], want.PitchState.gruState[:], 5e-2, label+" pitch gru_state")
	assertFloat32Close(t, got.pitch.state.xcorrMem1[:], want.PitchState.xcorrMem1[:], 5e-2, label+" pitch xcorr_mem1")
	assertFloat32Close(t, got.pitch.state.xcorrMem2[:], want.PitchState.xcorrMem2[:], 5e-2, label+" pitch xcorr_mem2")
	assertFloat32Close(t, got.pitch.state.xcorrMem3[:], want.PitchState.xcorrMem3[:], 5e-2, label+" pitch xcorr_mem3")
}

func dredParityAnalysisFrames(frameCount, frameSize int) []float32 {
	const (
		sampleRate = 48000
		dframeSize = 2 * FrameSize
	)
	var resampleMem [9]float32
	var inputBuffer [2 * dframeSize]float32
	inputFill := 79 + 12 - 80
	out := make([]float32, 0, frameCount*2*FrameSize)
	for frameIdx := 0; frameIdx < frameCount; frameIdx++ {
		pcm := make([]float64, frameSize)
		for i := 0; i < frameSize; i++ {
			pcm[i] = float64(dredParitySample(frameIdx, i, frameSize, sampleRate))
		}
		input := pcm
		remaining16k := frameSize * 16000 / sampleRate
		for remaining16k > 0 {
			processSize16k := dframeSize
			if processSize16k > remaining16k {
				processSize16k = remaining16k
			}
			processSize := processSize16k * sampleRate / 16000
			converted := dredParityConvert48kMono(input[:processSize], processSize16k, &resampleMem)
			copy(inputBuffer[inputFill:], converted)
			inputFill += processSize16k
			if inputFill >= dframeSize {
				out = append(out, inputBuffer[:dframeSize]...)
				inputFill -= dframeSize
				copy(inputBuffer[:inputFill], inputBuffer[dframeSize:dframeSize+inputFill])
			}
			input = input[processSize:]
			remaining16k -= processSize16k
		}
	}
	return out
}

func dredParitySample(frameIdx, sampleIdx, frameSize, sampleRate int) float32 {
	n := frameIdx*frameSize + sampleIdx
	tm := float64(n) / float64(sampleRate)
	env := 0.82 + 0.18*math.Sin(2*math.Pi*1.3*tm)
	s := 0.28*math.Sin(2*math.Pi*110*tm) +
		0.17*math.Sin(2*math.Pi*220*tm+0.11) +
		0.09*math.Sin(2*math.Pi*330*tm+0.23) +
		0.05*math.Sin(2*math.Pi*440*tm+0.37)
	return float32(env * s)
}

func dredParityConvert48kMono(in []float64, outLen int, mem *[9]float32) []float32 {
	const b0 = 0.004523418224
	b := [8]float32{0.005873358047, 0.012980854831, 0.014531340042, 0.014531340042, 0.012980854831, 0.005873358047, 0.004523418224, 0}
	a := [8]float32{-3.878718597768, 7.748834257468, -9.653651699533, 8.007342726666, -4.379450178552, 1.463182111810, -0.231720677804, 0}
	out := make([]float32, outLen)
	o := 0
	for i := range in {
		xi := float32(dredParityFloatToInt16(float32(in[i]))) + 1e-30
		yi := xi*b0 + mem[0]
		nyi := -yi
		for j := 0; j < 8; j++ {
			mem[j] = mem[j+1] + b[j]*xi + a[j]*nyi
		}
		if i%3 == 0 {
			out[o] = yi
			o++
		}
	}
	return out
}

func dredParityFloatToInt16(v float32) int16 {
	scaled := v * 32768
	if scaled > 32767 {
		return 32767
	}
	if scaled < -32768 {
		return -32768
	}
	return int16(math.RoundToEven(float64(scaled)))
}

func assertComplex64Close(t *testing.T, got, want []complex64, tol float64, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		assertFloat32Close(t, []float32{real(got[i]), imag(got[i])}, []float32{real(want[i]), imag(want[i])}, tol, label)
	}
}
