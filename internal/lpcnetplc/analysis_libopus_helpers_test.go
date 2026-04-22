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
	cmd := exec.Command(binPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run pitchdnn model blob helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

func probeLibopusPitchDNN(ifFeatures, xcorrFeatures []float32, state pitchDNNState) (float32, pitchDNNState, error) {
	binPath, err := getLibopusPitchDNNHelperPath()
	if err != nil {
		return 0, pitchDNNState{}, err
	}
	if len(ifFeatures) != pitchIFFeatures || len(xcorrFeatures) != pitchXcorrFeatures {
		return 0, pitchDNNState{}, fmt.Errorf("invalid pitchdnn helper input sizes")
	}
	var payload bytes.Buffer
	payload.WriteString(libopusPitchDNNInputMagic)
	if err := binary.Write(&payload, binary.LittleEndian, uint32(1)); err != nil {
		return 0, pitchDNNState{}, fmt.Errorf("encode pitchdnn helper version: %w", err)
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
		ifFeatures,
		xcorrFeatures,
		state.gruState[:],
		state.xcorrMem1[:],
		state.xcorrMem2[:],
		state.xcorrMem3[:],
	} {
		if err := writeBits(values); err != nil {
			return 0, pitchDNNState{}, fmt.Errorf("encode pitchdnn helper payload: %w", err)
		}
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, pitchDNNState{}, fmt.Errorf("run pitchdnn helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	data := stdout.Bytes()
	if len(data) < 8 || string(data[:4]) != libopusPitchDNNOutputMagic {
		return 0, pitchDNNState{}, fmt.Errorf("unexpected pitchdnn helper output")
	}
	offset := 8
	readBits := func(count int) ([]float32, error) {
		values := make([]float32, count)
		for i := 0; i < count; i++ {
			if len(data) < offset+4 {
				return nil, fmt.Errorf("truncated pitchdnn helper output")
			}
			values[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
			offset += 4
		}
		return values, nil
	}
	pitchValues, err := readBits(1)
	if err != nil {
		return 0, pitchDNNState{}, err
	}
	var next pitchDNNState
	for _, dst := range [][]float32{
		next.gruState[:],
		next.xcorrMem1[:],
		next.xcorrMem2[:],
		next.xcorrMem3[:],
	} {
		values, readErr := readBits(len(dst))
		if readErr != nil {
			return 0, pitchDNNState{}, readErr
		}
		copy(dst, values)
	}
	return pitchValues[0], next, nil
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

	var payload bytes.Buffer
	payload.WriteString(libopusLPCNetFeaturesInputMagic)
	if err := binary.Write(&payload, binary.LittleEndian, uint32(1)); err != nil {
		return libopusLPCNetFeaturesResult{}, fmt.Errorf("encode lpcnet helper version: %w", err)
	}
	if err := binary.Write(&payload, binary.LittleEndian, uint32(frameCount)); err != nil {
		return libopusLPCNetFeaturesResult{}, fmt.Errorf("encode lpcnet helper frame count: %w", err)
	}
	for _, v := range frames {
		if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(v)); err != nil {
			return libopusLPCNetFeaturesResult{}, fmt.Errorf("encode lpcnet helper frames: %w", err)
		}
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusLPCNetFeaturesResult{}, fmt.Errorf("run lpcnet helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	data := stdout.Bytes()
	if len(data) < 12 || string(data[:4]) != libopusLPCNetFeaturesOutputMagic {
		return libopusLPCNetFeaturesResult{}, fmt.Errorf("unexpected lpcnet helper output")
	}
	gotFrameCount := int(binary.LittleEndian.Uint32(data[8:12]))
	if gotFrameCount != frameCount {
		return libopusLPCNetFeaturesResult{}, fmt.Errorf("lpcnet helper frame count=%d want %d", gotFrameCount, frameCount)
	}
	offset := 12
	readBits := func(count int) ([]float32, error) {
		values := make([]float32, count)
		for i := 0; i < count; i++ {
			if len(data) < offset+4 {
				return nil, fmt.Errorf("truncated lpcnet helper output")
			}
			values[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
			offset += 4
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
	var payload bytes.Buffer
	payload.WriteString(libopusBurgInputMagic)
	if err := binary.Write(&payload, binary.LittleEndian, uint32(1)); err != nil {
		return nil, fmt.Errorf("encode burg helper version: %w", err)
	}
	for _, v := range frame {
		if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(v)); err != nil {
			return nil, fmt.Errorf("encode burg helper frame: %w", err)
		}
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run burg helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	data := stdout.Bytes()
	if len(data) < 8 || string(data[:4]) != libopusBurgOutputMagic {
		return nil, fmt.Errorf("unexpected burg helper output")
	}
	offset := 8
	out := make([]float32, 2*NumBands)
	for i := range out {
		if len(data) < offset+4 {
			return nil, fmt.Errorf("truncated burg helper output")
		}
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
	}
	return out, nil
}
