//go:build gopus_osce

package gopus

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
)

func TestOSCEBWEVariableSequenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helperPath, err := getLibopusOSCEBWEForwardHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE BWE sequence", err)
	}
	parsed, err := dnnblob.Clone(requireLibopusOSCEBWEModelBlob(t))
	if err != nil {
		t.Fatalf("dnnblob.Clone: %v", err)
	}
	const frames = 12
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

	payload := make([]byte, 12+2*len(input))
	copy(payload, "BSEQ")
	binary.LittleEndian.PutUint32(payload[4:8], 1)
	binary.LittleEndian.PutUint32(payload[8:12], frames)
	for i, v := range input {
		binary.LittleEndian.PutUint16(payload[12+2*i:], uint16(v))
	}
	out, err := libopustest.RunHelperArgs(helperPath, payload, "0", "sequence")
	if err != nil {
		t.Fatalf("OSCE BWE sequence oracle: %v", err)
	}
	reader, version, err := libopustest.NewOracleReaderMagicVersion("OSCE BWE sequence", "BSEQO\x00\x00\x00", out)
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
	const traceInts = 21
	if got := reader.I32(); got != traceFloats {
		t.Fatalf("helper state floats/frame=%d, want %d", got, traceFloats)
	}
	if got := reader.I32(); got != traceInts {
		t.Fatalf("helper delay ints/frame=%d, want %d", got, traceInts)
	}
	const latentFloats = 256
	if got := reader.I32(); got != latentFloats {
		t.Fatalf("helper latent floats/frame=%d, want %d", got, latentFloats)
	}
	const af3DenseFloats = 50
	if got := reader.I32(); got != af3DenseFloats {
		t.Fatalf("helper AF3 dense floats/frame=%d, want %d", got, af3DenseFloats)
	}
	reader.ExpectRemaining(frames*osceBWE.FeatureDim*4 + frames*latentFloats*4 + frames*af3DenseFloats*4 + frames*480*4 + frames*traceFloats*4 + frames*traceInts*2)
	wantFeatures := make([]float32, frames*osceBWE.FeatureDim)
	for i := range wantFeatures {
		wantFeatures[i] = reader.Float32()
	}
	for range frames * latentFloats {
		reader.Float32()
	}
	for range frames * af3DenseFloats {
		reader.Float32()
	}
	wantOutput := make([]float32, frames*480)
	for i := range wantOutput {
		wantOutput[i] = reader.Float32()
	}
	for range frames * traceFloats {
		reader.Float32()
	}
	for range frames * traceInts {
		reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	var featureState osceBWE.FeatureState
	featureState.Reset()
	var runtime osceBWE.State
	if err := runtime.SetModel(parsed); err != nil {
		t.Fatalf("State.SetModel: %v", err)
	}
	var inputFloat [160]float32
	var features [osceBWE.FeatureDim]float32
	var output [480]float32
	for frame := 0; frame < frames; frame++ {
		frameInput := input[frame*160 : frame*160+160]
		for i, v := range frameInput {
			inputFloat[i] = float32(v) / 32768
		}
		featureState.CalculateFeatures(features[:], frameInput)
		for i, got := range features {
			want := wantFeatures[frame*osceBWE.FeatureDim+i]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame=%d feature[%d] Go=%08x C=%08x", frame, i, math.Float32bits(got), math.Float32bits(want))
			}
		}
		if err := runtime.ProcessDelayed(inputFloat[:], output[:], features[:]); err != nil {
			t.Fatalf("frame=%d State.ProcessDelayed: %v", frame, err)
		}
		for i, got := range output {
			want := wantOutput[frame*480+i]
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("frame=%d sample=%d Go=%08x C=%08x", frame, i, math.Float32bits(got), math.Float32bits(want))
			}
		}
	}
}
