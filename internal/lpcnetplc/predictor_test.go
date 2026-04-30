package lpcnetplc

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func makePredictorTestBlob() []byte {
	var blob []byte
	for _, spec := range ModelLayerSpecs() {
		if spec.Bias != "" {
			blob = appendTestBlobRecord(blob, spec.Bias, dnnblob.TypeFloat, 4*spec.NbOutputs)
		}
		if spec.Subias != "" {
			blob = appendTestBlobRecord(blob, spec.Subias, dnnblob.TypeFloat, 4*spec.NbOutputs)
		}
		if spec.Scale != "" {
			blob = appendTestBlobRecord(blob, spec.Scale, dnnblob.TypeFloat, 4*spec.NbOutputs)
		}
		if spec.FloatWeights != "" {
			blob = appendTestBlobRecord(blob, spec.FloatWeights, dnnblob.TypeFloat, 4*spec.NbInputs*spec.NbOutputs)
		}
	}
	return blob
}

func appendTestBlobRecord(dst []byte, name string, typ int32, payloadSize int) []byte {
	const headerSize = 64
	blockSize := ((payloadSize + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(payloadSize))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	return append(dst, out...)
}

func newPredictorForTest(t *testing.T) *Predictor {
	t.Helper()
	blob, err := dnnblob.Clone(makePredictorTestBlob())
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	var predictor Predictor
	if err := predictor.SetModel(blob); err != nil {
		t.Fatalf("Predictor.SetModel error: %v", err)
	}
	return &predictor
}

func TestPredictorDoesNotAllocate(t *testing.T) {
	predictor := newPredictorForTest(t)
	var out [NumFeatures]float32
	var in [InputSize]float32
	for i := range in {
		in[i] = float32(i) / float32(len(in))
	}
	allocs := testing.AllocsPerRun(100, func() {
		if n := predictor.Predict(out[:], in[:]); n != NumFeatures {
			t.Fatalf("Predict()=%d want %d", n, NumFeatures)
		}
	})
	if allocs != 0 {
		t.Fatalf("Predict allocs/run=%v want 0", allocs)
	}
}

func TestQuantizeInputNearestEvenUsesFloat32Product(t *testing.T) {
	x := math.Float32frombits(0x3e870e1c)
	legacy := int16(math.RoundToEven(127 * float64(x)))
	want := int16(math.RoundToEven(float64(float32(127 * x))))
	if legacy != 33 || want != 34 {
		t.Fatalf("test input no longer straddles the arm64 quantizer boundary: legacy=%d want=%d", legacy, want)
	}

	if got := quantizeInputWithOptions(x, true, false); got != want {
		t.Fatalf("quantizeInput(%v)=%d want %d", x, got, want)
	}

	x86AVX2Want := int16(math.RoundToEven(float64(float32(math.FMA(float64(x), 127, 127)))))
	if got := quantizeInputWithOptions(x, true, true); got != x86AVX2Want {
		t.Fatalf("quantizeInput(%v) with avx2 subias=%d want %d", x, got, x86AVX2Want)
	}

	x86SSEWant := int16(127 + math.Floor(0.5+127*float64(x)))
	if got := quantizeInputWithOptions(x, false, true); got != x86SSEWant {
		t.Fatalf("quantizeInput(%v) with sse subias=%d want %d", x, got, x86SSEWant)
	}
}

func TestSGEMVUsesFusedFloatDenseWhenEnabled(t *testing.T) {
	values := []float32{
		1,
		math.Float32frombits(0x3f800001),
	}
	payload := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(payload[4*i:4*i+4], math.Float32bits(v))
	}
	weights, err := dnnblob.Float32ViewFromBytes(payload, int32(len(payload)))
	if err != nil {
		t.Fatalf("Float32ViewFromBytes error: %v", err)
	}
	x := []float32{
		1,
		math.Float32frombits(0x337fffff),
	}
	var out [1]float32

	sgemvSplit(out[:], weights, 1, 2, 1, x)
	split := out[0]

	sgemvFused(out[:], weights, 1, 2, 1, x)
	fused := out[0]

	if split != 1 {
		t.Fatalf("split sgemv=%g want 1", split)
	}
	if want := math.Float32frombits(0x3f800001); fused != want {
		t.Fatalf("fused sgemv=%g want %g", fused, want)
	}
}

func TestConsumeFECOrPredictUsesQueuedFeatureWithoutAllocating(t *testing.T) {
	predictor := newPredictorForTest(t)
	var state State
	var queued [NumFeatures]float32
	var out [NumFeatures]float32
	for i := range queued {
		queued[i] = float32(i + 1)
	}
	state.FECAdd(queued[:])
	allocs := testing.AllocsPerRun(100, func() {
		if gotFEC := state.ConsumeFECOrPredict(predictor, out[:]); !gotFEC {
			t.Fatal("ConsumeFECOrPredict()=predicted want queued FEC")
		}
		for i := range queued {
			if out[i] != queued[i] {
				t.Fatalf("out[%d]=%v want %v", i, out[i], queued[i])
			}
		}
		state.fecReadPos = 0
	})
	if allocs != 0 {
		t.Fatalf("ConsumeFECOrPredict allocs/run=%v want 0", allocs)
	}
}

func TestConsumeFECOrPredictFallsBackToPrediction(t *testing.T) {
	predictor := newPredictorForTest(t)
	var state State
	var out [NumFeatures]float32
	state.fecSkip = 2
	if gotFEC := state.ConsumeFECOrPredict(predictor, out[:]); gotFEC {
		t.Fatal("ConsumeFECOrPredict()=queued want predicted")
	}
	if state.fecSkip != 1 {
		t.Fatalf("fecSkip=%d want 1", state.fecSkip)
	}
}
