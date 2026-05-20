//go:build gopus_extra_controls
// +build gopus_extra_controls

package lpcnetplc

import (
	"fmt"
	"sync"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusPitchDNNInputMagic        = "GPDI"
	libopusPitchDNNOutputMagic       = "GPDO"
	libopusLPCNetFeaturesInputMagic  = "GLFI"
	libopusLPCNetFeaturesOutputMagic = "GLFO"
	libopusBurgInputMagic            = "GBCI"
	libopusBurgOutputMagic           = "GBCO"
)

var (
	libopusPitchDNNModelBlobHelperOnce sync.Once
	libopusPitchDNNModelBlobHelperPath string
	libopusPitchDNNModelBlobHelperErr  error

	libopusPitchDNNHelperOnce sync.Once
	libopusPitchDNNHelperPath string
	libopusPitchDNNHelperErr  error

	libopusLPCNetFeaturesHelperOnce sync.Once
	libopusLPCNetFeaturesHelperPath string
	libopusLPCNetFeaturesHelperErr  error

	libopusBurgHelperOnce sync.Once
	libopusBurgHelperPath string
	libopusBurgHelperErr  error
)

func getLibopusPitchDNNModelBlobHelperPath() (string, error) {
	libopusPitchDNNModelBlobHelperOnce.Do(func() {
		libopusPitchDNNModelBlobHelperPath, libopusPitchDNNModelBlobHelperErr = buildLibopusPLCHelper("libopus_pitchdnn_model_blob.c", "gopus_libopus_pitchdnn_model_blob")
	})
	if libopusPitchDNNModelBlobHelperErr != nil {
		return "", libopusPitchDNNModelBlobHelperErr
	}
	return libopusPitchDNNModelBlobHelperPath, nil
}

func getLibopusPitchDNNHelperPath() (string, error) {
	libopusPitchDNNHelperOnce.Do(func() {
		libopusPitchDNNHelperPath, libopusPitchDNNHelperErr = buildLibopusPLCHelper("libopus_pitchdnn_info.c", "gopus_libopus_pitchdnn")
	})
	if libopusPitchDNNHelperErr != nil {
		return "", libopusPitchDNNHelperErr
	}
	return libopusPitchDNNHelperPath, nil
}

func getLibopusLPCNetFeaturesHelperPath() (string, error) {
	libopusLPCNetFeaturesHelperOnce.Do(func() {
		libopusLPCNetFeaturesHelperPath, libopusLPCNetFeaturesHelperErr = buildLibopusPLCHelper("libopus_lpcnet_features_info.c", "gopus_libopus_lpcnet_features")
	})
	if libopusLPCNetFeaturesHelperErr != nil {
		return "", libopusLPCNetFeaturesHelperErr
	}
	return libopusLPCNetFeaturesHelperPath, nil
}

func getLibopusBurgHelperPath() (string, error) {
	libopusBurgHelperOnce.Do(func() {
		libopusBurgHelperPath, libopusBurgHelperErr = buildLibopusPLCHelper("libopus_burg_cepstrum_info.c", "gopus_libopus_burg_cepstrum")
	})
	if libopusBurgHelperErr != nil {
		return "", libopusBurgHelperErr
	}
	return libopusBurgHelperPath, nil
}

func probeLibopusPitchDNNModelBlob() ([]byte, error) {
	binPath, err := getLibopusPitchDNNModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run pitchdnn model blob helper: %w", err)
	}
	return out, nil
}

func probeLibopusPitchDNN(ifFeatures, xcorrFeatures []float32, state pitchDNNState) (float32, pitchDNNState, error) {
	binPath, err := getLibopusPitchDNNHelperPath()
	if err != nil {
		return 0, pitchDNNState{}, err
	}
	if len(ifFeatures) != pitchIFFeatures || len(xcorrFeatures) != pitchXcorrFeatures {
		return 0, pitchDNNState{}, fmt.Errorf("invalid pitchdnn helper input sizes")
	}
	payload := libopustest.NewOraclePayload(libopusPitchDNNInputMagic)
	for _, values := range [][]float32{
		ifFeatures,
		xcorrFeatures,
		state.gruState[:],
		state.xcorrMem1[:],
		state.xcorrMem2[:],
		state.xcorrMem3[:],
	} {
		payload.Float32s(values...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "pitchdnn", libopusPitchDNNOutputMagic)
	if err != nil {
		return 0, pitchDNNState{}, err
	}
	pitch := reader.Float32()
	var next pitchDNNState
	readBits := func(dst []float32) error {
		for i := range dst {
			dst[i] = reader.Float32()
		}
		return reader.Err()
	}
	for _, dst := range [][]float32{
		next.gruState[:],
		next.xcorrMem1[:],
		next.xcorrMem2[:],
		next.xcorrMem3[:],
	} {
		if err := readBits(dst); err != nil {
			return 0, pitchDNNState{}, err
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return 0, pitchDNNState{}, err
	}
	return pitch, next, nil
}

type libopusLPCNetFeaturesResult struct {
	Features    []float32
	AnalysisMem []float32
	MemPreemph  float32
	PrevIF      []complex64
	IFFeatures  []float32
	XCorr       []float32
	DNNPitch    float32
	PitchMem    []float32
	PitchFilt   float32
	ExcBuf      []float32
	LPBuf       []float32
	LPMem       []float32
	LPC         []float32
	PitchState  pitchDNNState
}

func probeLibopusLPCNetFeatures(frames []float32) (libopusLPCNetFeaturesResult, error) {
	binPath, err := getLibopusLPCNetFeaturesHelperPath()
	if err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	if len(frames) == 0 || len(frames)%FrameSize != 0 {
		return libopusLPCNetFeaturesResult{}, fmt.Errorf("frames length must be a positive multiple of FrameSize")
	}
	frameCount := len(frames) / FrameSize

	payload := libopustest.NewOraclePayload(libopusLPCNetFeaturesInputMagic, uint32(frameCount))
	payload.Float32s(frames...)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "lpcnet features", libopusLPCNetFeaturesOutputMagic)
	if err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	gotFrameCount := reader.Count(frameCount)
	if gotFrameCount != frameCount {
		return libopusLPCNetFeaturesResult{}, fmt.Errorf("lpcnet helper frame count=%d want %d", gotFrameCount, frameCount)
	}
	readBits := func(count int) ([]float32, error) {
		values := make([]float32, count)
		for i := 0; i < count; i++ {
			values[i] = reader.Float32()
		}
		if err := reader.Err(); err != nil {
			return nil, err
		}
		return values, nil
	}
	readComplex := func(count int) ([]complex64, error) {
		values := make([]complex64, count)
		for i := 0; i < count; i++ {
			re, err := readBits(1)
			if err != nil {
				return nil, err
			}
			im, err := readBits(1)
			if err != nil {
				return nil, err
			}
			values[i] = complex(re[0], im[0])
		}
		return values, nil
	}

	result := libopusLPCNetFeaturesResult{}
	if result.Features, err = readBits(frameCount * NumTotalFeatures); err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	if result.AnalysisMem, err = readBits(analysisOverlapSize); err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	memPreemph, err := readBits(1)
	if err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	result.MemPreemph = memPreemph[0]
	if result.PrevIF, err = readComplex(pitchIFMaxFreq); err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	if result.IFFeatures, err = readBits(pitchIFFeatures); err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	if result.XCorr, err = readBits(pitchXcorrFeatures); err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	dnnPitch, err := readBits(1)
	if err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	result.DNNPitch = dnnPitch[0]
	if result.PitchMem, err = readBits(analysisLPCOrder); err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	pitchFilt, err := readBits(1)
	if err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	result.PitchFilt = pitchFilt[0]
	if result.ExcBuf, err = readBits(analysisPitchBufSize); err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	if result.LPBuf, err = readBits(analysisPitchBufSize); err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	if result.LPMem, err = readBits(4); err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	if result.LPC, err = readBits(analysisLPCOrder); err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	for _, dst := range [][]float32{
		result.PitchState.gruState[:],
		result.PitchState.xcorrMem1[:],
		result.PitchState.xcorrMem2[:],
		result.PitchState.xcorrMem3[:],
	} {
		values, readErr := readBits(len(dst))
		if readErr != nil {
			return libopusLPCNetFeaturesResult{}, readErr
		}
		copy(dst, values)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusLPCNetFeaturesResult{}, err
	}
	return result, nil
}

func probeLibopusBurgCepstrum(frame []float32) ([]float32, error) {
	binPath, err := getLibopusBurgHelperPath()
	if err != nil {
		return nil, err
	}
	if len(frame) != FrameSize {
		return nil, fmt.Errorf("burg helper frame length=%d want %d", len(frame), FrameSize)
	}
	payload := libopustest.NewOraclePayload(libopusBurgInputMagic)
	payload.Float32s(frame...)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "burg cepstrum", libopusBurgOutputMagic)
	if err != nil {
		return nil, err
	}
	reader.ExpectRemaining(2 * NumBands * 4)
	out := make([]float32, 2*NumBands)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
