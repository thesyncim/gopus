package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func makeFARGANTestBlob() []byte {
	var blob []byte
	for _, spec := range FARGANModelLayerSpecs() {
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

func newFARGANForTest(t *testing.T) *FARGAN {
	t.Helper()
	blob, err := dnnblob.Clone(makeFARGANTestBlob())
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	var runtime FARGAN
	if err := runtime.SetModel(blob); err != nil {
		t.Fatalf("FARGAN.SetModel error: %v", err)
	}
	return &runtime
}

func fillFARGANPrimeInputs(pcm0 []float32, features0 []float32) {
	for i := range pcm0 {
		pcm0[i] = float32((i%29)-14) / 17
	}
	for i := range features0 {
		features0[i] = float32((i%13)-6) / 9
	}
}

func fillFARGANFeatures(features []float32) {
	for i := range features {
		features[i] = float32((i%11)-5) / 8
	}
}

func TestFARGANPrimeContinuityAndSynthesizeDoNotAllocate(t *testing.T) {
	runtime := newFARGANForTest(t)
	var pcm0 [FARGANContSamples]float32
	var contFeatures [ContVectors * NumFeatures]float32
	var frameFeatures [NumFeatures]float32
	var out [FARGANFrameSize]float32
	fillFARGANPrimeInputs(pcm0[:], contFeatures[:])
	fillFARGANFeatures(frameFeatures[:])

	allocs := testing.AllocsPerRun(100, func() {
		runtime.Reset()
		if n := runtime.PrimeContinuity(pcm0[:], contFeatures[:]); n != FARGANContSamples {
			t.Fatalf("PrimeContinuity()=%d want %d", n, FARGANContSamples)
		}
		if n := runtime.Synthesize(out[:], frameFeatures[:]); n != FARGANFrameSize {
			t.Fatalf("Synthesize()=%d want %d", n, FARGANFrameSize)
		}
	})
	if allocs != 0 {
		t.Fatalf("allocs/run=%v want 0", allocs)
	}
}

func TestFARGANLifecycleAndReset(t *testing.T) {
	runtime := newFARGANForTest(t)
	var pcm0 [FARGANContSamples]float32
	var contFeatures [ContVectors * NumFeatures]float32
	var frameFeatures [NumFeatures]float32
	var out [FARGANFrameSize]float32
	fillFARGANPrimeInputs(pcm0[:], contFeatures[:])
	fillFARGANFeatures(frameFeatures[:])

	if runtime.state.contInitialized {
		t.Fatal("contInitialized=true before PrimeContinuity")
	}
	if n := runtime.PrimeContinuity(pcm0[:], contFeatures[:]); n != FARGANContSamples {
		t.Fatalf("PrimeContinuity()=%d want %d", n, FARGANContSamples)
	}
	if !runtime.state.contInitialized {
		t.Fatal("contInitialized=false after PrimeContinuity")
	}
	if runtime.state.deemphMem != pcm0[FARGANContSamples-1] {
		t.Fatalf("deemphMem=%v want %v", runtime.state.deemphMem, pcm0[FARGANContSamples-1])
	}
	if runtime.state.lastPeriod == 0 {
		t.Fatal("lastPeriod not updated by PrimeContinuity")
	}
	if n := runtime.Synthesize(out[:], frameFeatures[:]); n != FARGANFrameSize {
		t.Fatalf("Synthesize()=%d want %d", n, FARGANFrameSize)
	}
	runtime.Reset()
	if runtime.state.contInitialized {
		t.Fatal("contInitialized=true after Reset()")
	}
	if runtime.state.lastPeriod != 0 || runtime.state.deemphMem != 0 {
		t.Fatalf("post-reset state=(lastPeriod=%d deemphMem=%v) want zero", runtime.state.lastPeriod, runtime.state.deemphMem)
	}
	for i, v := range runtime.state.pitchBuf {
		if v != 0 {
			t.Fatalf("pitchBuf[%d]=%v want 0 after Reset()", i, v)
		}
	}
}
