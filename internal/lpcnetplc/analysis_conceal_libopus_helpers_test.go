//go:build gopus_extra_controls

package lpcnetplc

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusPLCConcealAnalysisInputMagic  = "GPAI"
	libopusPLCConcealAnalysisOutputMagic = "GPAO"
)

var libopusPLCConcealAnalysisHelper libopustest.HelperCache

type libopusPLCConcealWithAnalysisResult struct {
	GotFEC   bool
	Frame    [FrameSize]float32
	State    StateSnapshot
	FARGAN   FARGANSnapshot
	Analysis libopusLPCNetFeaturesResult
}

func getLibopusPLCConcealAnalysisHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusPLCConcealAnalysisHelper, "libopus_plc_conceal_analysis_info.c", "gopus_libopus_plc_conceal_analysis")
}

func probeLibopusPLCConcealWithAnalysis(st *State, f *FARGAN, a *Analysis) (libopusPLCConcealWithAnalysisResult, error) {
	binPath, err := getLibopusPLCConcealAnalysisHelperPath()
	if err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	if st == nil || f == nil || a == nil {
		return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("nil conceal-analysis state")
	}

	snap := st.Snapshot()
	farganSnap := f.Snapshot()
	queueCount := st.FECFillPos()

	payload := libopustest.NewOraclePayload(libopusPLCConcealAnalysisInputMagic)
	payload.I32s(
		int32(snap.Blend),
		int32(snap.LossCount),
		int32(snap.AnalysisGap),
		int32(snap.AnalysisPos),
		int32(snap.PredictPos),
		int32(snap.FECReadPos),
		int32(snap.FECFillPos),
		int32(snap.FECSkip),
	)
	writeComplex := func(values []complex64) {
		for _, v := range values {
			payload.Float32(real(v))
			payload.Float32(imag(v))
		}
	}

	for _, values := range [][]float32{
		snap.Features[:],
		snap.Cont[:],
		snap.PCM[:],
		snap.PLCNet.GRU1[:],
		snap.PLCNet.GRU2[:],
		snap.PLCBak[0].GRU1[:],
		snap.PLCBak[0].GRU2[:],
		snap.PLCBak[1].GRU1[:],
		snap.PLCBak[1].GRU2[:],
	} {
		payload.Float32s(values...)
	}
	payload.I32(int32(func() int {
		if farganSnap.ContInitialized {
			return 1
		}
		return 0
	}()))
	payload.I32(int32(farganSnap.LastPeriod))
	for _, values := range [][]float32{
		{farganSnap.DeemphMem},
		farganSnap.PitchBuf[:],
		farganSnap.CondConv1State[:],
		farganSnap.FWC0Mem[:],
		farganSnap.GRU1State[:],
		farganSnap.GRU2State[:],
		farganSnap.GRU3State[:],
		a.analysisMem[:],
		{a.memPreemph},
	} {
		payload.Float32s(values...)
	}
	writeComplex(a.prevIF[:])
	for _, values := range [][]float32{
		a.ifFeatures[:],
		a.xcorrFeatures[:],
		{a.dnnPitch},
		a.pitchMem[:],
		{a.pitchFilt},
		a.excBuf[:],
		a.lpBuf[:],
		a.lpMem[:],
		a.lpc[:],
		a.pitch.state.gruState[:],
		a.pitch.state.xcorrMem1[:],
		a.pitch.state.xcorrMem2[:],
		a.pitch.state.xcorrMem3[:],
	} {
		payload.Float32s(values...)
	}
	payload.I32(int32(queueCount))
	var queued [NumFeatures]float32
	for i := 0; i < queueCount; i++ {
		if got := st.FillQueuedFeatures(i, queued[:]); got != NumFeatures {
			return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("FillQueuedFeatures(%d)=%d want %d", i, got, NumFeatures)
		}
		payload.Float32s(queued[:]...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "conceal-analysis", libopusPLCConcealAnalysisOutputMagic)
	if err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	readBits := func(dst []float32) error {
		return readLibopusFloat32Into(reader, dst)
	}
	readComplex := func(dst []complex64) error {
		for i := range dst {
			var pair [2]float32
			if err := readBits(pair[:]); err != nil {
				return err
			}
			dst[i] = complex(pair[0], pair[1])
		}
		return nil
	}

	var result libopusPLCConcealWithAnalysisResult
	result.GotFEC = reader.I32() != 0
	if err := readBits(result.Frame[:]); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	for _, dst := range []*int{
		&result.State.Blend,
		&result.State.LossCount,
		&result.State.AnalysisGap,
		&result.State.AnalysisPos,
		&result.State.PredictPos,
		&result.State.FECReadPos,
		&result.State.FECFillPos,
		&result.State.FECSkip,
	} {
		*dst = int(reader.I32())
	}
	for _, dst := range [][]float32{
		result.State.Features[:],
		result.State.Cont[:],
		result.State.PCM[:],
		result.State.PLCNet.GRU1[:],
		result.State.PLCNet.GRU2[:],
		result.State.PLCBak[0].GRU1[:],
		result.State.PLCBak[0].GRU2[:],
		result.State.PLCBak[1].GRU1[:],
		result.State.PLCBak[1].GRU2[:],
	} {
		if err := readBits(dst); err != nil {
			return libopusPLCConcealWithAnalysisResult{}, err
		}
	}
	result.FARGAN.ContInitialized = reader.I32() != 0
	result.FARGAN.LastPeriod = int(reader.I32())
	result.Analysis = libopusLPCNetFeaturesResult{
		AnalysisMem: make([]float32, analysisOverlapSize),
		PrevIF:      make([]complex64, pitchIFMaxFreq),
		IFFeatures:  make([]float32, pitchIFFeatures),
		XCorr:       make([]float32, pitchXcorrFeatures),
		PitchMem:    make([]float32, analysisLPCOrder),
		ExcBuf:      make([]float32, analysisPitchBufSize),
		LPBuf:       make([]float32, analysisPitchBufSize),
		LPMem:       make([]float32, 4),
		LPC:         make([]float32, analysisLPCOrder),
	}
	result.FARGAN.DeemphMem = reader.Float32()
	for _, dst := range [][]float32{
		result.FARGAN.PitchBuf[:],
		result.FARGAN.CondConv1State[:],
		result.FARGAN.FWC0Mem[:],
		result.FARGAN.GRU1State[:],
		result.FARGAN.GRU2State[:],
		result.FARGAN.GRU3State[:],
		result.Analysis.AnalysisMem,
	} {
		if err := readBits(dst); err != nil {
			return libopusPLCConcealWithAnalysisResult{}, err
		}
	}
	result.Analysis.MemPreemph = reader.Float32()
	if err := readComplex(result.Analysis.PrevIF); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	for _, dst := range [][]float32{
		result.Analysis.IFFeatures,
		result.Analysis.XCorr,
	} {
		if err := readBits(dst); err != nil {
			return libopusPLCConcealWithAnalysisResult{}, err
		}
	}
	result.Analysis.DNNPitch = reader.Float32()
	for _, dst := range [][]float32{
		result.Analysis.PitchMem,
	} {
		if err := readBits(dst); err != nil {
			return libopusPLCConcealWithAnalysisResult{}, err
		}
	}
	result.Analysis.PitchFilt = reader.Float32()
	for _, dst := range [][]float32{
		result.Analysis.ExcBuf,
		result.Analysis.LPBuf,
		result.Analysis.LPMem,
		result.Analysis.LPC,
		result.Analysis.PitchState.gruState[:],
		result.Analysis.PitchState.xcorrMem1[:],
		result.Analysis.PitchState.xcorrMem2[:],
		result.Analysis.PitchState.xcorrMem3[:],
	} {
		if err := readBits(dst); err != nil {
			return libopusPLCConcealWithAnalysisResult{}, err
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	return result, nil
}
