package lpcnetplc

import "testing"

func seedPredictorBackupsForTest(predictor *Predictor, st *State) {
	var tmpOut [NumFeatures]float32
	var seed1 [InputSize]float32
	var seed2 [InputSize]float32
	for i := 0; i < NumFeatures; i++ {
		seed1[2*NumBands+i] = float32((i%7)-3) / 11
		seed2[2*NumBands+i] = float32((i%5)-2) / 9
	}
	seed1[2*NumBands+NumFeatures] = -1
	seed2[2*NumBands+NumFeatures] = -1
	predictor.Reset()
	predictor.Predict(tmpOut[:], seed1[:])
	predictor.copyState(&st.plcBak[0])
	predictor.Predict(tmpOut[:], seed2[:])
	predictor.copyState(&st.plcBak[1])
	predictor.copyState(&st.plcNet)
	predictor.setState(&st.plcNet)
}

func seedBoundedConcealStateForTest(st *State) ([NumFeatures]float32, [NumFeatures]float32) {
	var fec0 [NumFeatures]float32
	var fec1 [NumFeatures]float32
	for i := 0; i < NumFeatures; i++ {
		fec0[i] = float32(i+1) / 13
		fec1[i] = float32((i%5)+1) / 7
	}
	st.FECClear()
	st.FECAdd(fec0[:])
	st.FECAdd(fec1[:])
	for i := range st.cont {
		st.cont[i] = float32((i%9)-4) / 10
	}
	for i := range st.pcm {
		st.pcm[i] = float32((i%23)-11) / 15
	}
	st.analysisGap = 1
	st.analysisPos = PLCBufSize
	st.predictPos = PLCBufSize
	st.blend = 0
	st.lossCount = 0
	return fec0, fec1
}

func TestStateLifecycle(t *testing.T) {
	var st State
	if st.Blend() != 0 {
		t.Fatalf("Blend()=%d want 0", st.Blend())
	}
	if st.AnalysisGap() != 1 || st.AnalysisPos() != PLCBufSize || st.PredictPos() != PLCBufSize {
		t.Fatalf("initial runtime state=(gap=%d analysis=%d predict=%d) want (1,%d,%d)", st.AnalysisGap(), st.AnalysisPos(), st.PredictPos(), PLCBufSize, PLCBufSize)
	}

	features := make([]float32, NumFeatures)
	for i := range features {
		features[i] = float32(i + 1)
	}
	st.FECAdd(features)
	st.FECAdd(nil)
	if st.FECFillPos() != 1 {
		t.Fatalf("FECFillPos()=%d want 1", st.FECFillPos())
	}
	if st.FECSkip() != 1 {
		t.Fatalf("FECSkip()=%d want 1", st.FECSkip())
	}

	var got [NumFeatures]float32
	if n := st.FillQueuedFeatures(0, got[:]); n != NumFeatures {
		t.Fatalf("FillQueuedFeatures count=%d want %d", n, NumFeatures)
	}
	for i, want := range features {
		if got[i] != want {
			t.Fatalf("queued[%d]=%v want %v", i, got[i], want)
		}
	}

	st.MarkConcealed()
	if st.Blend() != 1 {
		t.Fatalf("Blend()=%d want 1", st.Blend())
	}
	st.MarkUpdated()
	if st.Blend() != 0 {
		t.Fatalf("Blend()=%d want 0", st.Blend())
	}

	st.FECClear()
	if st.FECFillPos() != 0 || st.FECSkip() != 0 {
		t.Fatalf("post-clear = (fill=%d, skip=%d) want (0,0)", st.FECFillPos(), st.FECSkip())
	}

	st.MarkConcealed()
	st.Reset()
	if st.Blend() != 0 || st.FECFillPos() != 0 || st.FECSkip() != 0 || st.AnalysisGap() != 1 || st.AnalysisPos() != PLCBufSize || st.PredictPos() != PLCBufSize {
		t.Fatalf("post-reset = (blend=%d, fill=%d, skip=%d, gap=%d, analysis=%d, predict=%d) want (0,0,0,1,%d,%d)", st.Blend(), st.FECFillPos(), st.FECSkip(), st.AnalysisGap(), st.AnalysisPos(), st.PredictPos(), PLCBufSize, PLCBufSize)
	}
}

func TestFECAddDoesNotAllocate(t *testing.T) {
	var st State
	var features [NumFeatures]float32

	allocs := testing.AllocsPerRun(1000, func() {
		st.FECClear()
		st.FECAdd(features[:])
		st.FECAdd(nil)
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}

func TestQueueFeaturesAndCurrentFeatureLifecycle(t *testing.T) {
	var st State
	var features [NumFeatures]float32
	for i := range features {
		features[i] = float32(i + 1)
	}
	st.QueueFeatures(features[:])
	st.QueueFeatures(features[:])

	var gotCont [ContVectors * NumFeatures]float32
	if n := st.FillContFeatures(gotCont[:]); n != len(gotCont) {
		t.Fatalf("FillContFeatures count=%d want %d", n, len(gotCont))
	}
	base := (ContVectors - 2) * NumFeatures
	for i := 0; i < NumFeatures; i++ {
		if gotCont[base+i] != features[i] || gotCont[base+NumFeatures+i] != features[i] {
			t.Fatalf("queued continuity mismatch at %d", i)
		}
	}
}

func TestMarkUpdatedAndFinishConcealedFrameFloat(t *testing.T) {
	var st State
	var frame [FrameSize]float32
	for i := range frame {
		frame[i] = float32(i + 1)
	}
	if n := st.MarkUpdatedFrameFloat(frame[:]); n != FrameSize {
		t.Fatalf("MarkUpdatedFrameFloat=%d want %d", n, FrameSize)
	}
	if st.Blend() != 0 || st.LossCount() != 0 || st.AnalysisPos() != PLCBufSize-FrameSize || st.PredictPos() != PLCBufSize-FrameSize {
		t.Fatalf("post-update state mismatch")
	}

	var gotPCM [PLCBufSize]float32
	if n := st.FillPCMHistory(gotPCM[:]); n != PLCBufSize {
		t.Fatalf("FillPCMHistory count=%d want %d", n, PLCBufSize)
	}
	for i := 0; i < FrameSize; i++ {
		if gotPCM[PLCBufSize-FrameSize+i] != frame[i] {
			t.Fatalf("pcm tail[%d]=%v want %v", i, gotPCM[PLCBufSize-FrameSize+i], frame[i])
		}
	}

	if n := st.FinishConcealedFrameFloat(frame[:]); n != FrameSize {
		t.Fatalf("FinishConcealedFrameFloat=%d want %d", n, FrameSize)
	}
	if st.Blend() != 1 || st.PredictPos() != PLCBufSize {
		t.Fatalf("post-conceal state mismatch: blend=%d predict=%d", st.Blend(), st.PredictPos())
	}
}

func TestPredictorBackedConcealmentStepsDoNotAllocate(t *testing.T) {
	predictor := newPredictorForTest(t)
	var st State
	var features [NumFeatures]float32
	for i := range features {
		features[i] = float32(i + 1)
	}
	st.FECClear()
	st.FECAdd(features[:])
	st.FECAdd(features[:])
	st.SyncPredictor(predictor)

	allocs := testing.AllocsPerRun(200, func() {
		var local State
		local = st
		predictor.Reset()
		local.SyncPredictor(predictor)
		local.PrimeFirstLossPrefill(predictor)
		local.ConcealmentFeatureStep(predictor)
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}

func TestBoundedConcealFrameFloatDoesNotAllocate(t *testing.T) {
	predictor := newPredictorForTest(t)
	fargan := newFARGANForTest(t)
	var st State
	seedPredictorBackupsForTest(predictor, &st)
	seedBoundedConcealStateForTest(&st)

	allocs := testing.AllocsPerRun(100, func() {
		localState := st
		localPredictor := *predictor
		localFARGAN := *fargan
		var frame [FrameSize]float32
		localPredictor.setState(&localState.plcNet)
		localState.ConcealFrameFloat(&localPredictor, &localFARGAN, frame[:])
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}
