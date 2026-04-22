//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package lpcnetplc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"sync"
)

const (
	libopusPLCConcealAnalysisInputMagic  = "GPAI"
	libopusPLCConcealAnalysisOutputMagic = "GPAO"
)

var (
	libopusPLCConcealAnalysisHelperOnce sync.Once
	libopusPLCConcealAnalysisHelperPath string
	libopusPLCConcealAnalysisHelperErr  error
)

type libopusPLCConcealWithAnalysisResult struct {
	GotFEC   bool
	Frame    [FrameSize]float32
	State    StateSnapshot
	FARGAN   FARGANSnapshot
	Analysis libopusLPCNetFeaturesResult
}

func getLibopusPLCConcealAnalysisHelperPath() (string, error) {
	libopusPLCConcealAnalysisHelperOnce.Do(func() {
		libopusPLCConcealAnalysisHelperPath, libopusPLCConcealAnalysisHelperErr = buildLibopusPLCHelper("libopus_plc_conceal_analysis_info.c", "gopus_libopus_plc_conceal_analysis")
	})
	if libopusPLCConcealAnalysisHelperErr != nil {
		return "", libopusPLCConcealAnalysisHelperErr
	}
	return libopusPLCConcealAnalysisHelperPath, nil
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

	var payload bytes.Buffer
	payload.WriteString(libopusPLCConcealAnalysisInputMagic)
	if err := binary.Write(&payload, binary.LittleEndian, uint32(1)); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("encode conceal-analysis version: %w", err)
	}
	for _, v := range []int32{
		int32(snap.Blend),
		int32(snap.LossCount),
		int32(snap.AnalysisGap),
		int32(snap.AnalysisPos),
		int32(snap.PredictPos),
		int32(snap.FECReadPos),
		int32(snap.FECFillPos),
		int32(snap.FECSkip),
	} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("encode conceal-analysis header: %w", err)
		}
	}

	writeBits := func(values []float32) error {
		for _, v := range values {
			if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(v)); err != nil {
				return err
			}
		}
		return nil
	}
	writeComplex := func(values []complex64) error {
		for _, v := range values {
			if err := writeBits([]float32{real(v), imag(v)}); err != nil {
				return err
			}
		}
		return nil
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
		if err := writeBits(values); err != nil {
			return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("encode conceal-analysis plc payload: %w", err)
		}
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(func() int {
		if farganSnap.ContInitialized {
			return 1
		}
		return 0
	}())); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("encode conceal-analysis fargan init: %w", err)
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(farganSnap.LastPeriod)); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("encode conceal-analysis fargan period: %w", err)
	}
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
		if err := writeBits(values); err != nil {
			return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("encode conceal-analysis state payload: %w", err)
		}
	}
	if err := writeComplex(a.prevIF[:]); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("encode conceal-analysis prev_if: %w", err)
	}
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
		if err := writeBits(values); err != nil {
			return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("encode conceal-analysis analysis payload: %w", err)
		}
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(queueCount)); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("encode conceal-analysis queue count: %w", err)
	}
	var queued [NumFeatures]float32
	for i := 0; i < queueCount; i++ {
		if got := st.FillQueuedFeatures(i, queued[:]); got != NumFeatures {
			return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("FillQueuedFeatures(%d)=%d want %d", i, got, NumFeatures)
		}
		if err := writeBits(queued[:]); err != nil {
			return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("encode conceal-analysis queue payload: %w", err)
		}
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("run conceal-analysis helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	data := stdout.Bytes()
	if len(data) < 12 || string(data[:4]) != libopusPLCConcealAnalysisOutputMagic {
		return libopusPLCConcealWithAnalysisResult{}, fmt.Errorf("unexpected conceal-analysis helper output")
	}
	offset := 8
	readBits := func(dst []float32) error {
		for i := range dst {
			if len(data) < offset+4 {
				return fmt.Errorf("truncated conceal-analysis helper output")
			}
			dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
			offset += 4
		}
		return nil
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
	readI32 := func() (int32, error) {
		if len(data) < offset+4 {
			return 0, fmt.Errorf("truncated conceal-analysis helper i32")
		}
		v := int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
		return v, nil
	}

	var result libopusPLCConcealWithAnalysisResult
	gotFEC, err := readI32()
	if err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	result.GotFEC = gotFEC != 0
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
		v, readErr := readI32()
		if readErr != nil {
			return libopusPLCConcealWithAnalysisResult{}, readErr
		}
		*dst = int(v)
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
	contInit, err := readI32()
	if err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	result.FARGAN.ContInitialized = contInit != 0
	lastPeriod, err := readI32()
	if err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	result.FARGAN.LastPeriod = int(lastPeriod)
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
	deemph := make([]float32, 1)
	if err := readBits(deemph); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	result.FARGAN.DeemphMem = deemph[0]
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
	memPreemph := make([]float32, 1)
	if err := readBits(memPreemph); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	result.Analysis.MemPreemph = memPreemph[0]
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
	dnnPitch := make([]float32, 1)
	if err := readBits(dnnPitch); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	result.Analysis.DNNPitch = dnnPitch[0]
	for _, dst := range [][]float32{
		result.Analysis.PitchMem,
	} {
		if err := readBits(dst); err != nil {
			return libopusPLCConcealWithAnalysisResult{}, err
		}
	}
	pitchFilt := make([]float32, 1)
	if err := readBits(pitchFilt); err != nil {
		return libopusPLCConcealWithAnalysisResult{}, err
	}
	result.Analysis.PitchFilt = pitchFilt[0]
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
	return result, nil
}
