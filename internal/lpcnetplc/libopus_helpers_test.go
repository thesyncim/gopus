//go:build gopus_osce

package lpcnetplc

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusPLCPredictInputMagic   = "GPLI"
	libopusPLCPredictOutputMagic  = "GPLO"
	libopusPLCUpdateInputMagic    = "GPUI"
	libopusPLCUpdateOutputMagic   = "GPUO"
	libopusPLCPrefillInputMagic   = "GPPI"
	libopusPLCPrefillOutputMagic  = "GPPO"
	libopusPLCConcealInputMagic   = "GPCI"
	libopusPLCConcealOutputMagic  = "GPCO"
	libopusFARGANCondInputMagic   = "GFCI"
	libopusFARGANCondOutputMagic  = "GFCO"
	libopusFARGANContInputMagic   = "GFC0"
	libopusFARGANContOutputMagic  = "GFO0"
	libopusFARGANSynthInputMagic  = "GFSI"
	libopusFARGANSynthOutputMagic = "GFSO"
)

var (
	libopusPLCModelBlobHelper libopustest.HelperCache
	libopusPLCPredictHelper   libopustest.HelperCache
	libopusPLCUpdateHelper    libopustest.HelperCache
	libopusPLCPrefillHelper   libopustest.HelperCache
	libopusPLCConcealHelper   libopustest.HelperCache
	libopusFARGANModelHelper  libopustest.HelperCache
	libopusFARGANCondHelper   libopustest.HelperCache
	libopusFARGANContHelper   libopustest.HelperCache
	libopusFARGANSynthHelper  libopustest.HelperCache
)

func buildLibopusPLCHelper(sourceFile, outputBase string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	return libopustest.BuildDREDHelper(repoRoot, sourceFile, outputBase, true)
}

func cachedLibopusPLCHelperPath(cache *libopustest.HelperCache, sourceFile, outputBase string) (string, error) {
	return cache.Path(func() (string, error) {
		return buildLibopusPLCHelper(sourceFile, outputBase)
	})
}

func getLibopusPLCModelBlobHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusPLCModelBlobHelper, "libopus_plc_model_blob.c", "gopus_libopus_plc_model_blob")
}

func getLibopusPLCPredictHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusPLCPredictHelper, "libopus_plc_pred_info.c", "gopus_libopus_plc_pred")
}

func getLibopusPLCUpdateHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusPLCUpdateHelper, "libopus_plc_update_info.c", "gopus_libopus_plc_update")
}

func getLibopusPLCPrefillHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusPLCPrefillHelper, "libopus_plc_prefill_info.c", "gopus_libopus_plc_prefill")
}

func getLibopusPLCConcealHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusPLCConcealHelper, "libopus_plc_conceal_info.c", "gopus_libopus_plc_conceal")
}

func getLibopusFARGANModelBlobHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusFARGANModelHelper, "libopus_fargan_model_blob.c", "gopus_libopus_fargan_model_blob")
}

func getLibopusFARGANCondHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusFARGANCondHelper, "libopus_fargan_cond_info.c", "gopus_libopus_fargan_cond")
}

func getLibopusFARGANContHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusFARGANContHelper, "libopus_fargan_cont_info.c", "gopus_libopus_fargan_cont")
}

func getLibopusFARGANSynthHelperPath() (string, error) {
	return cachedLibopusPLCHelperPath(&libopusFARGANSynthHelper, "libopus_fargan_synth_info.c", "gopus_libopus_fargan_synth")
}

func probeLibopusPLCModelBlob() ([]byte, error) {
	binPath, err := getLibopusPLCModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run plc model blob helper: %w", err)
	}
	return out, nil
}

func probeLibopusFARGANModelBlob() ([]byte, error) {
	binPath, err := getLibopusFARGANModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run fargan model blob helper: %w", err)
	}
	return out, nil
}

func probeLibopusPLCPredict(input []float32, gru1State, gru2State []float32) (out, nextGRU1, nextGRU2 []float32, err error) {
	binPath, err := getLibopusPLCPredictHelperPath()
	if err != nil {
		return nil, nil, nil, err
	}
	if len(input) != InputSize || len(gru1State) != GRU1Size || len(gru2State) != GRU2Size {
		return nil, nil, nil, fmt.Errorf("invalid helper input sizes")
	}

	payload := libopustest.NewOraclePayload(libopusPLCPredictInputMagic)
	payload.Float32s(input...)
	payload.Float32s(gru1State...)
	payload.Float32s(gru2State...)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "plc predict", libopusPLCPredictOutputMagic)
	if err != nil {
		return nil, nil, nil, err
	}
	out, err = readLibopusFloat32s(reader, NumFeatures)
	if err != nil {
		return nil, nil, nil, err
	}
	nextGRU1, err = readLibopusFloat32s(reader, GRU1Size)
	if err != nil {
		return nil, nil, nil, err
	}
	nextGRU2, err = readLibopusFloat32s(reader, GRU2Size)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, nil, err
	}
	return out, nextGRU1, nextGRU2, nil
}

func readLibopusFloat32s(reader *libopustest.OracleReader, count int) ([]float32, error) {
	values := make([]float32, count)
	for i := range values {
		values[i] = reader.Float32()
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func readLibopusFloat32Into(reader *libopustest.OracleReader, dst []float32) error {
	for i := range dst {
		dst[i] = reader.Float32()
	}
	return reader.Err()
}

type libopusPLCUpdateResult struct {
	Blend       int
	LossCount   int
	AnalysisGap int
	AnalysisPos int
	PredictPos  int
	PCM         []float32
}

func probeLibopusPLCUpdate(state State, frame []float32) (libopusPLCUpdateResult, error) {
	binPath, err := getLibopusPLCUpdateHelperPath()
	if err != nil {
		return libopusPLCUpdateResult{}, err
	}
	if len(frame) != FrameSize {
		return libopusPLCUpdateResult{}, fmt.Errorf("invalid update helper frame size")
	}
	payload := libopustest.NewOraclePayload(libopusPLCUpdateInputMagic)
	payload.I32s(
		int32(state.blend),
		int32(state.lossCount),
		int32(state.analysisGap),
		int32(state.analysisPos),
		int32(state.predictPos),
	)
	payload.Float32s(state.pcm[:]...)
	payload.Float32s(frame[:FrameSize]...)

	return runLibopusPLCUpdate(binPath, payload.Bytes(), 1)
}

func probeLibopusPLCUpdateInt16(state State, frame []int16) (libopusPLCUpdateResult, error) {
	binPath, err := getLibopusPLCUpdateHelperPath()
	if err != nil {
		return libopusPLCUpdateResult{}, err
	}
	if len(frame) != FrameSize {
		return libopusPLCUpdateResult{}, fmt.Errorf("invalid update helper frame size")
	}
	payload := libopustest.NewOraclePayloadVersion(libopusPLCUpdateInputMagic, 2)
	payload.I32s(
		int32(state.blend),
		int32(state.lossCount),
		int32(state.analysisGap),
		int32(state.analysisPos),
		int32(state.predictPos),
	)
	payload.Float32s(state.pcm[:]...)
	for _, v := range frame[:FrameSize] {
		payload.I16(v)
	}

	return runLibopusPLCUpdate(binPath, payload.Bytes(), 2)
}

func runLibopusPLCUpdate(binPath string, payload []byte, version uint32) (libopusPLCUpdateResult, error) {
	reader, err := libopustest.RunOracleVersion(binPath, payload, "plc update", libopusPLCUpdateOutputMagic, version)
	if err != nil {
		return libopusPLCUpdateResult{}, err
	}
	result := libopusPLCUpdateResult{
		Blend:       int(reader.I32()),
		LossCount:   int(reader.I32()),
		AnalysisGap: int(reader.I32()),
		AnalysisPos: int(reader.I32()),
		PredictPos:  int(reader.I32()),
	}
	result.PCM = make([]float32, PLCBufSize)
	if err := readLibopusFloat32Into(reader, result.PCM); err != nil {
		return libopusPLCUpdateResult{}, err
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusPLCUpdateResult{}, err
	}
	return result, nil
}

type libopusPLCPrefillResult struct {
	LossCount int
	FECRead   int
	FECSkip   int
	Features  []float32
	Cont      []float32
	PLCNet    predictorState
	PLCBak    [2]predictorState
}

func probeLibopusPLCPrefill(features, cont, fec0, fec1 []float32, plcNet predictorState, plcBak [2]predictorState, fecFillPos, fecSkip, lossCount int, runPrime, runConceal bool) (libopusPLCPrefillResult, error) {
	binPath, err := getLibopusPLCPrefillHelperPath()
	if err != nil {
		return libopusPLCPrefillResult{}, err
	}
	if len(features) != NumTotalFeatures || len(cont) != ContVectors*NumFeatures || len(fec0) != NumFeatures || len(fec1) != NumFeatures {
		return libopusPLCPrefillResult{}, fmt.Errorf("invalid helper input sizes")
	}
	var flags uint32
	if runPrime {
		flags |= 1
	}
	if runConceal {
		flags |= 2
	}

	payload := libopustest.NewOraclePayload(libopusPLCPrefillInputMagic, flags)
	payload.I32s(int32(lossCount), int32(fecFillPos), int32(fecSkip))
	for _, values := range [][]float32{
		features,
		cont,
		plcNet.gru1[:],
		plcNet.gru2[:],
		plcBak[0].gru1[:],
		plcBak[0].gru2[:],
		plcBak[1].gru1[:],
		plcBak[1].gru2[:],
		fec0,
		fec1,
	} {
		payload.Float32s(values...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "plc prefill", libopusPLCPrefillOutputMagic)
	if err != nil {
		return libopusPLCPrefillResult{}, err
	}
	result := libopusPLCPrefillResult{
		LossCount: int(reader.I32()),
		FECRead:   int(reader.I32()),
		FECSkip:   int(reader.I32()),
	}
	if result.Features, err = readLibopusFloat32s(reader, NumTotalFeatures); err != nil {
		return libopusPLCPrefillResult{}, err
	}
	if result.Cont, err = readLibopusFloat32s(reader, ContVectors*NumFeatures); err != nil {
		return libopusPLCPrefillResult{}, err
	}
	for _, dst := range [][]float32{
		result.PLCNet.gru1[:],
		result.PLCNet.gru2[:],
		result.PLCBak[0].gru1[:],
		result.PLCBak[0].gru2[:],
		result.PLCBak[1].gru1[:],
		result.PLCBak[1].gru2[:],
	} {
		if err := readLibopusFloat32Into(reader, dst); err != nil {
			return libopusPLCPrefillResult{}, err
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusPLCPrefillResult{}, err
	}
	return result, nil
}

type libopusPLCConcealResult struct {
	GotFEC      bool
	Blend       int
	LossCount   int
	AnalysisGap int
	AnalysisPos int
	PredictPos  int
	FECRead     int
	FECSkip     int
	Frame       []float32
	Features    []float32
	Cont        []float32
	PCM         []float32
	PLCNet      predictorState
	PLCBak      [2]predictorState
	FARGAN      libopusFARGANRuntimeResult
}

func probeLibopusPLCConceal(state State, farganState FARGANState, fec0, fec1 []float32) (libopusPLCConcealResult, error) {
	binPath, err := getLibopusPLCConcealHelperPath()
	if err != nil {
		return libopusPLCConcealResult{}, err
	}
	if len(fec0) != NumFeatures || len(fec1) != NumFeatures {
		return libopusPLCConcealResult{}, fmt.Errorf("invalid conceal helper FEC sizes")
	}

	payload := libopustest.NewOraclePayloadVersion(libopusPLCConcealInputMagic, 2)
	payload.I32s(
		int32(state.blend),
		int32(state.lossCount),
		int32(state.analysisGap),
		int32(state.analysisPos),
		int32(state.predictPos),
		int32(state.fecReadPos),
		int32(state.fecFillPos),
		int32(state.fecSkip),
	)
	for _, values := range [][]float32{
		state.features[:],
		state.cont[:],
		state.pcm[:],
		state.plcNet.gru1[:],
		state.plcNet.gru2[:],
		state.plcBak[0].gru1[:],
		state.plcBak[0].gru2[:],
		state.plcBak[1].gru1[:],
		state.plcBak[1].gru2[:],
	} {
		payload.Float32s(values...)
	}
	var contInitialized int32
	if farganState.contInitialized {
		contInitialized = 1
	}
	payload.I32s(contInitialized, int32(farganState.lastPeriod), 2)
	for _, values := range [][]float32{
		{farganState.deemphMem},
		farganState.pitchBuf[:],
		farganState.condConv1State[:],
		farganState.fwc0Mem[:],
		farganState.gru1State[:],
		farganState.gru2State[:],
		farganState.gru3State[:],
	} {
		payload.Float32s(values...)
	}
	for _, values := range [][]float32{
		fec0,
		fec1,
	} {
		payload.Float32s(values...)
	}

	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "plc conceal", libopusPLCConcealOutputMagic, 2)
	if err != nil {
		return libopusPLCConcealResult{}, err
	}
	result := libopusPLCConcealResult{
		GotFEC:      reader.I32() != 0,
		Blend:       int(reader.I32()),
		LossCount:   int(reader.I32()),
		AnalysisGap: int(reader.I32()),
		AnalysisPos: int(reader.I32()),
		PredictPos:  int(reader.I32()),
		FECRead:     int(reader.I32()),
		FECSkip:     int(reader.I32()),
	}
	result.FARGAN.ContInitialized = reader.I32() != 0
	result.FARGAN.LastPeriod = int(reader.I32())
	if result.Frame, err = readLibopusFloat32s(reader, FrameSize); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.Features, err = readLibopusFloat32s(reader, NumTotalFeatures); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.Cont, err = readLibopusFloat32s(reader, ContVectors*NumFeatures); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.PCM, err = readLibopusFloat32s(reader, PLCBufSize); err != nil {
		return libopusPLCConcealResult{}, err
	}
	for _, dst := range [][]float32{
		result.PLCNet.gru1[:],
		result.PLCNet.gru2[:],
		result.PLCBak[0].gru1[:],
		result.PLCBak[0].gru2[:],
		result.PLCBak[1].gru1[:],
		result.PLCBak[1].gru2[:],
	} {
		if err := readLibopusFloat32Into(reader, dst); err != nil {
			return libopusPLCConcealResult{}, err
		}
	}
	result.FARGAN.DeemphMem = reader.Float32()
	if result.FARGAN.PitchBuf, err = readLibopusFloat32s(reader, PitchMaxPeriod); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.FARGAN.CondConv1State, err = readLibopusFloat32s(reader, FARGANCondConv1State); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.FARGAN.FWC0Mem, err = readLibopusFloat32s(reader, SigNetFWC0StateSize); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.FARGAN.GRU1State, err = readLibopusFloat32s(reader, SigNetGRU1StateSize); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.FARGAN.GRU2State, err = readLibopusFloat32s(reader, SigNetGRU2StateSize); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.FARGAN.GRU3State, err = readLibopusFloat32s(reader, SigNetGRU3StateSize); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusPLCConcealResult{}, err
	}
	return result, nil
}

func probeLibopusFARGANCond(features []float32, period int, condConv1State []float32) (cond, nextState []float32, err error) {
	binPath, err := getLibopusFARGANCondHelperPath()
	if err != nil {
		return nil, nil, err
	}
	if len(features) != NumFeatures || len(condConv1State) != FARGANCondConv1State {
		return nil, nil, fmt.Errorf("invalid helper input sizes")
	}

	payload := libopustest.NewOraclePayload(libopusFARGANCondInputMagic)
	payload.I32(int32(period))
	payload.Float32s(features...)
	payload.Float32s(condConv1State...)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fargan cond", libopusFARGANCondOutputMagic)
	if err != nil {
		return nil, nil, err
	}
	cond, err = readLibopusFloat32s(reader, FARGANCondDense2Size)
	if err != nil {
		return nil, nil, err
	}
	nextState, err = readLibopusFloat32s(reader, FARGANCondConv1State)
	if err != nil {
		return nil, nil, err
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	return cond, nextState, nil
}

type libopusFARGANRuntimeResult struct {
	ContInitialized bool
	LastPeriod      int
	DeemphMem       float32
	PCM             []float32
	PitchBuf        []float32
	CondConv1State  []float32
	FWC0Mem         []float32
	GRU1State       []float32
	GRU2State       []float32
	GRU3State       []float32
}

func probeLibopusFARGANContinuity(pcm0, features0 []float32) (libopusFARGANRuntimeResult, error) {
	binPath, err := getLibopusFARGANContHelperPath()
	if err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if len(pcm0) != FARGANContSamples || len(features0) != ContVectors*NumFeatures {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("invalid continuity helper input sizes")
	}

	payload := libopustest.NewOraclePayload(libopusFARGANContInputMagic)
	payload.Float32s(pcm0...)
	payload.Float32s(features0...)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fargan continuity", libopusFARGANContOutputMagic)
	if err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	return readLibopusFARGANRuntimeResult(reader, false)
}

func probeLibopusFARGANSynthesize(state FARGANState, features []float32) (libopusFARGANRuntimeResult, error) {
	binPath, err := getLibopusFARGANSynthHelperPath()
	if err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if len(features) != NumFeatures {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("invalid synth helper input sizes")
	}

	payload := libopustest.NewOraclePayload(libopusFARGANSynthInputMagic)
	var contInitialized int32
	if state.contInitialized {
		contInitialized = 1
	}
	payload.I32s(contInitialized, int32(state.lastPeriod))
	for _, values := range [][]float32{
		{state.deemphMem},
		state.pitchBuf[:],
		state.condConv1State[:],
		state.fwc0Mem[:],
		state.gru1State[:],
		state.gru2State[:],
		state.gru3State[:],
		features,
	} {
		payload.Float32s(values...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fargan synth", libopusFARGANSynthOutputMagic)
	if err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	return readLibopusFARGANRuntimeResult(reader, true)
}

func readLibopusFARGANRuntimeResult(reader *libopustest.OracleReader, withPCM bool) (libopusFARGANRuntimeResult, error) {
	result := libopusFARGANRuntimeResult{
		ContInitialized: reader.I32() != 0,
		LastPeriod:      int(reader.I32()),
	}
	result.DeemphMem = reader.Float32()
	if withPCM {
		var err error
		result.PCM, err = readLibopusFloat32s(reader, FARGANFrameSize)
		if err != nil {
			return libopusFARGANRuntimeResult{}, err
		}
	}
	var err error
	if result.PitchBuf, err = readLibopusFloat32s(reader, PitchMaxPeriod); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if result.CondConv1State, err = readLibopusFloat32s(reader, FARGANCondConv1State); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if result.FWC0Mem, err = readLibopusFloat32s(reader, SigNetFWC0StateSize); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if result.GRU1State, err = readLibopusFloat32s(reader, SigNetGRU1StateSize); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if result.GRU2State, err = readLibopusFloat32s(reader, SigNetGRU2StateSize); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if result.GRU3State, err = readLibopusFloat32s(reader, SigNetGRU3StateSize); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	return result, nil
}
