//go:build gopus_osce

package bwe

import (
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var bweSequenceOracle libopustest.HelperCache
var bweModelBlobOracle libopustest.HelperCache

func TestBWEVariableSequenceStateMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	wd, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(wd, "../../.."))
	modelHelper, err := bweModelBlobOracle.Path(func() (string, error) {
		return libopustest.BuildOSCEHelper(root, "libopus_osce_bwe_model_blob.c", "gopus_bwe_sequence_model_blob", true)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE BWE model blob", err)
	}
	modelBytes, err := libopustest.RunHelper(modelHelper, nil)
	if err != nil {
		t.Fatalf("OSCE BWE model blob: %v", err)
	}
	model, err := dnnblob.Clone(modelBytes)
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}

	const frames = 12
	input := makeBWESequenceInput(frames)
	payload := make([]byte, 12+2*len(input))
	copy(payload, "BSEQ")
	binary.LittleEndian.PutUint32(payload[4:8], 1)
	binary.LittleEndian.PutUint32(payload[8:12], frames)
	for i, v := range input {
		binary.LittleEndian.PutUint16(payload[12+2*i:], uint16(v))
	}
	helperPath, err := bweSequenceOracle.Path(func() (string, error) {
		return libopustest.BuildOSCEHelper(root, "libopus_osce_bwe_forward.c", "gopus_bwe_sequence_state_oracle", true)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE BWE sequence state", err)
	}
	out, err := libopustest.RunHelperArgs(helperPath, payload, "0", "sequence")
	if err != nil {
		t.Fatalf("OSCE BWE sequence state oracle: %v", err)
	}
	reader, version, err := libopustest.NewOracleReaderMagicVersion("OSCE BWE sequence state", "BSEQO\x00\x00\x00", out)
	if err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatalf("helper version=%d, want 4", version)
	}
	if got := reader.I32(); got != frames {
		t.Fatalf("helper frames=%d, want %d", got, frames)
	}
	if got := reader.I32(); got != frames*480 {
		t.Fatalf("helper output samples=%d, want %d", got, frames*480)
	}
	const traceFloats = 1701
	const traceInts = outputDelaySamples
	if got := reader.I32(); got != traceFloats {
		t.Fatalf("helper state floats/frame=%d, want %d", got, traceFloats)
	}
	if got := reader.I32(); got != traceInts {
		t.Fatalf("helper delay ints/frame=%d, want %d", got, traceInts)
	}
	const latentFloats = 2 * CondDim
	if got := reader.I32(); got != latentFloats {
		t.Fatalf("helper latent floats/frame=%d, want %d", got, latentFloats)
	}
	const af3DenseFloats = AF3KernelOut + 2*AF3GainOut
	if got := reader.I32(); got != af3DenseFloats {
		t.Fatalf("helper AF3 dense floats/frame=%d, want %d", got, af3DenseFloats)
	}
	reader.ExpectRemaining(frames*FeatureDim*4 + frames*latentFloats*4 + frames*af3DenseFloats*4 + frames*480*4 + frames*traceFloats*4 + frames*traceInts*2)
	wantFeatures := make([]float32, frames*FeatureDim)
	for i := range wantFeatures {
		wantFeatures[i] = reader.Float32()
	}
	wantLatent := make([]float32, frames*latentFloats)
	for i := range wantLatent {
		wantLatent[i] = reader.Float32()
	}
	wantAF3Dense := make([]float32, frames*af3DenseFloats)
	for i := range wantAF3Dense {
		wantAF3Dense[i] = reader.Float32()
	}
	wantOutput := make([]float32, frames*480)
	for i := range wantOutput {
		wantOutput[i] = reader.Float32()
	}
	wantState := make([]float32, frames*traceFloats)
	for i := range wantState {
		wantState[i] = reader.Float32()
	}
	wantDelay := make([]int16, frames*traceInts)
	for i := range wantDelay {
		wantDelay[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	var featureState FeatureState
	featureState.Reset()
	var runtime State
	if err := runtime.SetModel(model); err != nil {
		t.Fatalf("State.SetModel: %v", err)
	}
	var inputFloat [160]float32
	var features [FeatureDim]float32
	var output [480]float32
	for frame := 0; frame < frames; frame++ {
		frameInput := input[frame*160 : frame*160+160]
		for i, v := range frameInput {
			inputFloat[i] = float32(v) / 32768
		}
		featureState.CalculateFeatures(features[:], frameInput)
		for i, got := range features {
			want := wantFeatures[frame*FeatureDim+i]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame=%d feature[%d] Go=%08x C=%08x", frame, i, math.Float32bits(got), math.Float32bits(want))
			}
		}
		latentState := runtime
		var latent [4 * CondDim]float32
		latentState.featureNet(latent[:2*CondDim], features[:], 1)
		for i, got := range latent[:latentFloats] {
			want := wantLatent[frame*latentFloats+i]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame=%d latent[%d] Go=%08x C=%08x", frame, i, math.Float32bits(got), math.Float32bits(want))
			}
		}
		var af3Dense [AF3KernelOut + 2*AF3GainOut]float32
		frameLatent := latent[CondDim : 2*CondDim]
		computeGenericDense(&runtime.model.AF3Kernel, af3Dense[:AF3KernelOut], frameLatent, actLinear)
		computeLinear(&runtime.model.AF3Gain, af3Dense[AF3KernelOut:AF3KernelOut+AF3GainOut], frameLatent)
		computeGenericDense(&runtime.model.AF3Gain, af3Dense[AF3KernelOut+AF3GainOut:], frameLatent, actTanh)
		for i, got := range af3Dense {
			want := wantAF3Dense[frame*af3DenseFloats+i]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame=%d AF3 dense[%d] Go=%08x C=%08x", frame, i, math.Float32bits(got), math.Float32bits(want))
			}
		}
		if err := runtime.ProcessDelayed(inputFloat[:], output[:], features[:]); err != nil {
			t.Fatalf("frame=%d State.ProcessDelayed: %v", frame, err)
		}
		compareBWESequenceState(t, frame, &runtime, wantState[frame*traceFloats:(frame+1)*traceFloats])
		for i, got := range output {
			want := wantOutput[frame*480+i]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame=%d output[%d] Go=%08x C=%08x", frame, i, math.Float32bits(got), math.Float32bits(want))
			}
		}
		for i, got := range runtime.outputDelay {
			want := wantDelay[frame*traceInts+i]
			if got != want {
				t.Fatalf("frame=%d outputDelay[%d] Go=%d C=%d", frame, i, got, want)
			}
		}
	}
}

func makeBWESequenceInput(frames int) []int16 {
	input := make([]int16, frames*160)
	for frame := 0; frame < frames; frame++ {
		for i := 0; i < 160; i++ {
			n := frame*160 + i
			v := 0.47*math.Sin(2*math.Pi*313*float64(n)/16000) +
				0.18*math.Sin(2*math.Pi*719*float64(n)/16000+0.23) +
				0.08*math.Sin(2*math.Pi*97*float64(n)/16000+float64(frame)*0.11)
			q := int(math.Round(v * 32767))
			if q > 32767 {
				q = 32767
			} else if q < -32768 {
				q = -32768
			}
			input[n] = int16(q)
		}
	}
	return input
}

func compareBWESequenceState(t *testing.T, frame int, s *State, want []float32) {
	t.Helper()
	var got [1701]float32
	idx := 0
	appendPart := func(name string, values []float32) {
		t.Helper()
		for i, value := range values {
			if idx >= len(got) {
				t.Fatalf("frame=%d state trace overflow at %s[%d]", frame, name, i)
			}
			got[idx] = value
			idx++
		}
	}
	appendPart("fnetConv1", s.fnetConv1State[:])
	appendPart("fnetConv2", s.fnetConv2State[:])
	appendPart("fnetGRU", s.fnetGRUState[:])
	appendPart("af1.history", s.af1History[:])
	appendPart("af1.lastKernel", s.af1LastKernel[:])
	appendPart("af1.lastGain", []float32{0})
	appendPart("af2.history", s.af2History[:])
	appendPart("af2.lastKernel", s.af2LastKernel[:])
	appendPart("af2.lastGain", []float32{0})
	appendPart("af3.history", s.af3History[:])
	appendPart("af3.lastKernel", s.af3LastKernel[:])
	appendPart("af3.lastGain", []float32{0})
	appendPart("tdshape1.alpha1F", s.tdshape1Alpha1FState[:])
	appendPart("tdshape1.alpha1T", s.tdshape1Alpha1TState[:])
	appendPart("tdshape1.alpha2", s.tdshape1Alpha2State[:])
	appendPart("tdshape1.interpolate", []float32{s.tdshape1InterpState})
	appendPart("tdshape2.alpha1F", s.tdshape2Alpha1FState[:])
	appendPart("tdshape2.alpha1T", s.tdshape2Alpha1TState[:])
	appendPart("tdshape2.alpha2", s.tdshape2Alpha2State[:])
	appendPart("tdshape2.interpolate", []float32{s.tdshape2InterpState})
	for ch := range 3 {
		appendPart("resampler.upsampEven", s.resampUpEven[ch][:])
		appendPart("resampler.upsampOdd", s.resampUpOdd[ch][:])
		appendPart("resampler.interpol", s.resampInterpol[ch][:])
	}
	if idx != len(got) {
		t.Fatalf("frame=%d Go state trace has %d floats, want %d", frame, idx, len(got))
	}
	if len(want) != len(got) {
		t.Fatalf("frame=%d C state trace has %d floats, want %d", frame, len(want), len(got))
	}
	for i, value := range got {
		if math.Float32bits(value) != math.Float32bits(want[i]) {
			t.Fatalf("frame=%d state[%d] Go=%08x C=%08x", frame, i, math.Float32bits(value), math.Float32bits(want[i]))
		}
	}
}
