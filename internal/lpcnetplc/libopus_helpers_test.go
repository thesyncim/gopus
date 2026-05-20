//go:build gopus_extra_controls
// +build gopus_extra_controls

package lpcnetplc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

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
	libopusPLCModelBlobHelperOnce sync.Once
	libopusPLCModelBlobHelperPath string
	libopusPLCModelBlobHelperErr  error

	libopusPLCPredictHelperOnce sync.Once
	libopusPLCPredictHelperPath string
	libopusPLCPredictHelperErr  error

	libopusPLCUpdateHelperOnce sync.Once
	libopusPLCUpdateHelperPath string
	libopusPLCUpdateHelperErr  error

	libopusPLCPrefillHelperOnce sync.Once
	libopusPLCPrefillHelperPath string
	libopusPLCPrefillHelperErr  error

	libopusPLCConcealHelperOnce sync.Once
	libopusPLCConcealHelperPath string
	libopusPLCConcealHelperErr  error

	libopusFARGANModelBlobHelperOnce sync.Once
	libopusFARGANModelBlobHelperPath string
	libopusFARGANModelBlobHelperErr  error

	libopusFARGANCondHelperOnce sync.Once
	libopusFARGANCondHelperPath string
	libopusFARGANCondHelperErr  error

	libopusFARGANContHelperOnce sync.Once
	libopusFARGANContHelperPath string
	libopusFARGANContHelperErr  error

	libopusFARGANSynthHelperOnce sync.Once
	libopusFARGANSynthHelperPath string
	libopusFARGANSynthHelperErr  error
)

func buildLibopusPLCHelper(sourceFile, outputBase string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	return libopustest.BuildDREDHelper(repoRoot, sourceFile, outputBase, true)
}

func getLibopusPLCModelBlobHelperPath() (string, error) {
	libopusPLCModelBlobHelperOnce.Do(func() {
		libopusPLCModelBlobHelperPath, libopusPLCModelBlobHelperErr = buildLibopusPLCHelper("libopus_plc_model_blob.c", "gopus_libopus_plc_model_blob")
	})
	if libopusPLCModelBlobHelperErr != nil {
		return "", libopusPLCModelBlobHelperErr
	}
	return libopusPLCModelBlobHelperPath, nil
}

func getLibopusPLCPredictHelperPath() (string, error) {
	libopusPLCPredictHelperOnce.Do(func() {
		libopusPLCPredictHelperPath, libopusPLCPredictHelperErr = buildLibopusPLCHelper("libopus_plc_pred_info.c", "gopus_libopus_plc_pred")
	})
	if libopusPLCPredictHelperErr != nil {
		return "", libopusPLCPredictHelperErr
	}
	return libopusPLCPredictHelperPath, nil
}

func getLibopusPLCUpdateHelperPath() (string, error) {
	libopusPLCUpdateHelperOnce.Do(func() {
		libopusPLCUpdateHelperPath, libopusPLCUpdateHelperErr = buildLibopusPLCHelper("libopus_plc_update_info.c", "gopus_libopus_plc_update")
	})
	if libopusPLCUpdateHelperErr != nil {
		return "", libopusPLCUpdateHelperErr
	}
	return libopusPLCUpdateHelperPath, nil
}

func getLibopusPLCPrefillHelperPath() (string, error) {
	libopusPLCPrefillHelperOnce.Do(func() {
		libopusPLCPrefillHelperPath, libopusPLCPrefillHelperErr = buildLibopusPLCHelper("libopus_plc_prefill_info.c", "gopus_libopus_plc_prefill")
	})
	if libopusPLCPrefillHelperErr != nil {
		return "", libopusPLCPrefillHelperErr
	}
	return libopusPLCPrefillHelperPath, nil
}

func getLibopusPLCConcealHelperPath() (string, error) {
	libopusPLCConcealHelperOnce.Do(func() {
		libopusPLCConcealHelperPath, libopusPLCConcealHelperErr = buildLibopusPLCHelper("libopus_plc_conceal_info.c", "gopus_libopus_plc_conceal")
	})
	if libopusPLCConcealHelperErr != nil {
		return "", libopusPLCConcealHelperErr
	}
	return libopusPLCConcealHelperPath, nil
}

func getLibopusFARGANModelBlobHelperPath() (string, error) {
	libopusFARGANModelBlobHelperOnce.Do(func() {
		libopusFARGANModelBlobHelperPath, libopusFARGANModelBlobHelperErr = buildLibopusPLCHelper("libopus_fargan_model_blob.c", "gopus_libopus_fargan_model_blob")
	})
	if libopusFARGANModelBlobHelperErr != nil {
		return "", libopusFARGANModelBlobHelperErr
	}
	return libopusFARGANModelBlobHelperPath, nil
}

func getLibopusFARGANCondHelperPath() (string, error) {
	libopusFARGANCondHelperOnce.Do(func() {
		libopusFARGANCondHelperPath, libopusFARGANCondHelperErr = buildLibopusPLCHelper("libopus_fargan_cond_info.c", "gopus_libopus_fargan_cond")
	})
	if libopusFARGANCondHelperErr != nil {
		return "", libopusFARGANCondHelperErr
	}
	return libopusFARGANCondHelperPath, nil
}

func getLibopusFARGANContHelperPath() (string, error) {
	libopusFARGANContHelperOnce.Do(func() {
		libopusFARGANContHelperPath, libopusFARGANContHelperErr = buildLibopusPLCHelper("libopus_fargan_cont_info.c", "gopus_libopus_fargan_cont")
	})
	if libopusFARGANContHelperErr != nil {
		return "", libopusFARGANContHelperErr
	}
	return libopusFARGANContHelperPath, nil
}

func getLibopusFARGANSynthHelperPath() (string, error) {
	libopusFARGANSynthHelperOnce.Do(func() {
		libopusFARGANSynthHelperPath, libopusFARGANSynthHelperErr = buildLibopusPLCHelper("libopus_fargan_synth_info.c", "gopus_libopus_fargan_synth")
	})
	if libopusFARGANSynthHelperErr != nil {
		return "", libopusFARGANSynthHelperErr
	}
	return libopusFARGANSynthHelperPath, nil
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

	var payload bytes.Buffer
	payload.WriteString(libopusPLCConcealInputMagic)
	if err := binary.Write(&payload, binary.LittleEndian, uint32(2)); err != nil {
		return libopusPLCConcealResult{}, fmt.Errorf("encode plc conceal version: %w", err)
	}
	for _, v := range []int32{
		int32(state.blend),
		int32(state.lossCount),
		int32(state.analysisGap),
		int32(state.analysisPos),
		int32(state.predictPos),
		int32(state.fecReadPos),
		int32(state.fecFillPos),
		int32(state.fecSkip),
	} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusPLCConcealResult{}, fmt.Errorf("encode plc conceal header: %w", err)
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
		if err := writeBits(values); err != nil {
			return libopusPLCConcealResult{}, fmt.Errorf("encode plc conceal state: %w", err)
		}
	}
	var contInitialized int32
	if farganState.contInitialized {
		contInitialized = 1
	}
	if err := binary.Write(&payload, binary.LittleEndian, contInitialized); err != nil {
		return libopusPLCConcealResult{}, fmt.Errorf("encode plc conceal fargan flag: %w", err)
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(farganState.lastPeriod)); err != nil {
		return libopusPLCConcealResult{}, fmt.Errorf("encode plc conceal fargan last period: %w", err)
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(2)); err != nil {
		return libopusPLCConcealResult{}, fmt.Errorf("encode plc conceal queue count: %w", err)
	}
	for _, values := range [][]float32{
		{farganState.deemphMem},
		farganState.pitchBuf[:],
		farganState.condConv1State[:],
		farganState.fwc0Mem[:],
		farganState.gru1State[:],
		farganState.gru2State[:],
		farganState.gru3State[:],
	} {
		if err := writeBits(values); err != nil {
			return libopusPLCConcealResult{}, fmt.Errorf("encode plc conceal fargan payload: %w", err)
		}
	}
	for _, values := range [][]float32{
		fec0,
		fec1,
	} {
		if err := writeBits(values); err != nil {
			return libopusPLCConcealResult{}, fmt.Errorf("encode plc conceal queue payload: %w", err)
		}
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusPLCConcealResult{}, fmt.Errorf("run plc conceal helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	data := stdout.Bytes()
	const header = 48
	if len(data) < header || string(data[:4]) != libopusPLCConcealOutputMagic {
		return libopusPLCConcealResult{}, fmt.Errorf("unexpected plc conceal helper output")
	}
	result := libopusPLCConcealResult{
		GotFEC:      int32(binary.LittleEndian.Uint32(data[8:12])) != 0,
		Blend:       int(int32(binary.LittleEndian.Uint32(data[12:16]))),
		LossCount:   int(int32(binary.LittleEndian.Uint32(data[16:20]))),
		AnalysisGap: int(int32(binary.LittleEndian.Uint32(data[20:24]))),
		AnalysisPos: int(int32(binary.LittleEndian.Uint32(data[24:28]))),
		PredictPos:  int(int32(binary.LittleEndian.Uint32(data[28:32]))),
		FECRead:     int(int32(binary.LittleEndian.Uint32(data[32:36]))),
		FECSkip:     int(int32(binary.LittleEndian.Uint32(data[36:40]))),
	}
	result.FARGAN.ContInitialized = int32(binary.LittleEndian.Uint32(data[40:44])) != 0
	result.FARGAN.LastPeriod = int(int32(binary.LittleEndian.Uint32(data[44:48])))
	offset := header
	readBits := func(count int) ([]float32, error) {
		values := make([]float32, count)
		for i := 0; i < count; i++ {
			if len(data) < offset+4 {
				return nil, fmt.Errorf("truncated plc conceal helper output")
			}
			values[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
			offset += 4
		}
		return values, nil
	}
	if result.Frame, err = readBits(FrameSize); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.Features, err = readBits(NumTotalFeatures); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.Cont, err = readBits(ContVectors * NumFeatures); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.PCM, err = readBits(PLCBufSize); err != nil {
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
		values, readErr := readBits(len(dst))
		if readErr != nil {
			return libopusPLCConcealResult{}, readErr
		}
		copy(dst, values)
	}
	values, err := readBits(1)
	if err != nil {
		return libopusPLCConcealResult{}, err
	}
	result.FARGAN.DeemphMem = values[0]
	if result.FARGAN.PitchBuf, err = readBits(PitchMaxPeriod); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.FARGAN.CondConv1State, err = readBits(FARGANCondConv1State); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.FARGAN.FWC0Mem, err = readBits(SigNetFWC0StateSize); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.FARGAN.GRU1State, err = readBits(SigNetGRU1StateSize); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.FARGAN.GRU2State, err = readBits(SigNetGRU2StateSize); err != nil {
		return libopusPLCConcealResult{}, err
	}
	if result.FARGAN.GRU3State, err = readBits(SigNetGRU3StateSize); err != nil {
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

	var payload bytes.Buffer
	payload.WriteString(libopusFARGANCondInputMagic)
	if err := binary.Write(&payload, binary.LittleEndian, uint32(1)); err != nil {
		return nil, nil, fmt.Errorf("encode fargan helper version: %w", err)
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(period)); err != nil {
		return nil, nil, fmt.Errorf("encode fargan helper period: %w", err)
	}
	writeBits := func(values []float32) error {
		for _, v := range values {
			if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(v)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeBits(features); err != nil {
		return nil, nil, fmt.Errorf("encode fargan helper features: %w", err)
	}
	if err := writeBits(condConv1State); err != nil {
		return nil, nil, fmt.Errorf("encode fargan helper state: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("run fargan cond helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	data := stdout.Bytes()
	const header = 8
	if len(data) < header || string(data[:4]) != libopusFARGANCondOutputMagic {
		return nil, nil, fmt.Errorf("unexpected fargan cond helper output")
	}
	offset := header
	readBits := func(count int) ([]float32, error) {
		values := make([]float32, count)
		for i := 0; i < count; i++ {
			if len(data) < offset+4 {
				return nil, fmt.Errorf("truncated fargan cond helper output")
			}
			values[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
			offset += 4
		}
		return values, nil
	}
	cond, err = readBits(FARGANCondDense2Size)
	if err != nil {
		return nil, nil, err
	}
	nextState, err = readBits(FARGANCondConv1State)
	if err != nil {
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

	var payload bytes.Buffer
	payload.WriteString(libopusFARGANContInputMagic)
	if err := binary.Write(&payload, binary.LittleEndian, uint32(1)); err != nil {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("encode fargan continuity version: %w", err)
	}
	writeBits := func(values []float32) error {
		for _, v := range values {
			if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(v)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeBits(pcm0); err != nil {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("encode fargan continuity pcm0: %w", err)
	}
	if err := writeBits(features0); err != nil {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("encode fargan continuity features: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("run fargan continuity helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return readLibopusFARGANRuntimeResult(stdout.Bytes(), libopusFARGANContOutputMagic, false)
}

func probeLibopusFARGANSynthesize(state FARGANState, features []float32) (libopusFARGANRuntimeResult, error) {
	binPath, err := getLibopusFARGANSynthHelperPath()
	if err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if len(features) != NumFeatures {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("invalid synth helper input sizes")
	}

	var payload bytes.Buffer
	payload.WriteString(libopusFARGANSynthInputMagic)
	if err := binary.Write(&payload, binary.LittleEndian, uint32(1)); err != nil {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("encode fargan synth version: %w", err)
	}
	var contInitialized int32
	if state.contInitialized {
		contInitialized = 1
	}
	if err := binary.Write(&payload, binary.LittleEndian, contInitialized); err != nil {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("encode fargan synth cont flag: %w", err)
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(state.lastPeriod)); err != nil {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("encode fargan synth last period: %w", err)
	}
	writeBits := func(values []float32) error {
		for _, v := range values {
			if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(v)); err != nil {
				return err
			}
		}
		return nil
	}
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
		if err := writeBits(values); err != nil {
			return libopusFARGANRuntimeResult{}, fmt.Errorf("encode fargan synth payload: %w", err)
		}
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("run fargan synth helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return readLibopusFARGANRuntimeResult(stdout.Bytes(), libopusFARGANSynthOutputMagic, true)
}

func readLibopusFARGANRuntimeResult(data []byte, magic string, withPCM bool) (libopusFARGANRuntimeResult, error) {
	const header = 16
	if len(data) < header || string(data[:4]) != magic {
		return libopusFARGANRuntimeResult{}, fmt.Errorf("unexpected fargan runtime helper output")
	}
	result := libopusFARGANRuntimeResult{
		ContInitialized: int32(binary.LittleEndian.Uint32(data[8:12])) != 0,
		LastPeriod:      int(int32(binary.LittleEndian.Uint32(data[12:16]))),
	}
	offset := header
	readBits := func(count int) ([]float32, error) {
		values := make([]float32, count)
		for i := 0; i < count; i++ {
			if len(data) < offset+4 {
				return nil, fmt.Errorf("truncated fargan runtime helper output")
			}
			values[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
			offset += 4
		}
		return values, nil
	}
	values, err := readBits(1)
	if err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	result.DeemphMem = values[0]
	if withPCM {
		result.PCM, err = readBits(FARGANFrameSize)
		if err != nil {
			return libopusFARGANRuntimeResult{}, err
		}
	}
	if result.PitchBuf, err = readBits(PitchMaxPeriod); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if result.CondConv1State, err = readBits(FARGANCondConv1State); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if result.FWC0Mem, err = readBits(SigNetFWC0StateSize); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if result.GRU1State, err = readBits(SigNetGRU1StateSize); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if result.GRU2State, err = readBits(SigNetGRU2StateSize); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	if result.GRU3State, err = readBits(SigNetGRU3StateSize); err != nil {
		return libopusFARGANRuntimeResult{}, err
	}
	return result, nil
}
