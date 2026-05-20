//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package gopus

import (
	"fmt"
	"sync"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/libopustest"
)

type libopusDREDProcessInfo struct {
	availableSamples  int
	dredEndSamples    int
	processRet        int
	processStage      int
	nbLatents         int
	dredOffset        int
	secondProcessRet  int
	secondStage       int
	cloneProcessRet   int
	cloneStage        int
	secondStateHash   uint32
	secondLatentHash  uint32
	secondFeatureHash uint32
	cloneStateHash    uint32
	cloneLatentHash   uint32
	cloneFeatureHash  uint32
	state             [internaldred.StateDim]float32
	latents           []float32
	features          []float32
}

type libopusDREDRecoveryWindowInfo struct {
	availableSamples         int
	dredEndSamples           int
	processRet               int
	processStage             int
	nbLatents                int
	dredOffset               int
	featuresPerFrame         int
	neededFeatureFrames      int
	featureOffsetBase        int
	maxFeatureIndex          int
	recoverableFeatureFrames int
	missingPositiveFrames    int
	featureOffsets           []int
}

var (
	libopusDREDModelBlobHelperOnce sync.Once
	libopusDREDModelBlobHelperPath string
	libopusDREDModelBlobHelperErr  error

	libopusDREDProcessHelperOnce sync.Once
	libopusDREDProcessHelperPath string
	libopusDREDProcessHelperErr  error

	libopusDREDRecoveryWindowHelperOnce sync.Once
	libopusDREDRecoveryWindowHelperPath string
	libopusDREDRecoveryWindowHelperErr  error
)

func getLibopusDREDModelBlobHelperPath() (string, error) {
	libopusDREDModelBlobHelperOnce.Do(func() {
		libopusDREDModelBlobHelperPath, libopusDREDModelBlobHelperErr = buildLibopusDREDHelper("libopus_dred_model_blob.c", "gopus_libopus_dred_model_blob", true)
	})
	if libopusDREDModelBlobHelperErr != nil {
		return "", libopusDREDModelBlobHelperErr
	}
	return libopusDREDModelBlobHelperPath, nil
}

func getLibopusDREDProcessHelperPath() (string, error) {
	libopusDREDProcessHelperOnce.Do(func() {
		libopusDREDProcessHelperPath, libopusDREDProcessHelperErr = buildLibopusDREDHelper("libopus_dred_process_info.c", "gopus_libopus_dred_process", true)
	})
	if libopusDREDProcessHelperErr != nil {
		return "", libopusDREDProcessHelperErr
	}
	return libopusDREDProcessHelperPath, nil
}

func getLibopusDREDRecoveryWindowHelperPath() (string, error) {
	libopusDREDRecoveryWindowHelperOnce.Do(func() {
		libopusDREDRecoveryWindowHelperPath, libopusDREDRecoveryWindowHelperErr = buildLibopusDREDHelper("libopus_dred_recovery_window_info.c", "gopus_libopus_dred_recovery_window", true)
	})
	if libopusDREDRecoveryWindowHelperErr != nil {
		return "", libopusDREDRecoveryWindowHelperErr
	}
	return libopusDREDRecoveryWindowHelperPath, nil
}

func probeLibopusDREDModelBlob() ([]byte, error) {
	binPath, err := getLibopusDREDModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run dred model blob helper: %w", err)
	}
	return out, nil
}

func probeLibopusDREDProcess(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDProcessInfo, error) {
	binPath, err := getLibopusDREDProcessHelperPath()
	if err != nil {
		return libopusDREDProcessInfo{}, err
	}

	payload := libopustest.NewOraclePayload(libopusDREDParseInputMagic, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet)))
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDProcessInfo{}, fmt.Errorf("run dred process helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred process", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDProcessInfo{}, err
	}

	info := libopusDREDProcessInfo{
		availableSamples:  int(reader.I32()),
		dredEndSamples:    int(reader.I32()),
		processRet:        int(reader.I32()),
		processStage:      int(reader.I32()),
		nbLatents:         int(reader.I32()),
		dredOffset:        int(reader.I32()),
		secondProcessRet:  int(reader.I32()),
		secondStage:       int(reader.I32()),
		cloneProcessRet:   int(reader.I32()),
		cloneStage:        int(reader.I32()),
		secondStateHash:   reader.U32(),
		secondLatentHash:  reader.U32(),
		secondFeatureHash: reader.U32(),
		cloneStateHash:    reader.U32(),
		cloneLatentHash:   reader.U32(),
		cloneFeatureHash:  reader.U32(),
	}

	for i := range info.state {
		info.state[i] = reader.Float32()
	}

	latentValues := info.nbLatents * internaldred.LatentStride
	info.latents = make([]float32, latentValues)
	for i := 0; i < latentValues; i++ {
		info.latents[i] = reader.Float32()
	}

	featureValues := info.nbLatents * 4 * internaldred.NumFeatures
	info.features = make([]float32, featureValues)
	for i := 0; i < featureValues; i++ {
		info.features[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDProcessInfo{}, err
	}
	return info, nil
}

func probeLibopusDREDRecoveryWindow(packet []byte, maxDREDSamples, sampleRate, frameSizeSamples, decodeOffsetSamples int, blend bool) (libopusDREDRecoveryWindowInfo, error) {
	binPath, err := getLibopusDREDRecoveryWindowHelperPath()
	if err != nil {
		return libopusDREDRecoveryWindowInfo{}, err
	}

	var blendFlag uint32
	if blend {
		blendFlag = 1
	}
	payload := libopustest.NewOraclePayload(
		libopusDREDParseInputMagic,
		uint32(sampleRate),
		uint32(maxDREDSamples),
		uint32(frameSizeSamples),
		uint32(int32(decodeOffsetSamples)),
		blendFlag,
		uint32(len(packet)),
	)
	payload.Raw(packet)

	out, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusDREDRecoveryWindowInfo{}, fmt.Errorf("run dred recovery helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("dred recovery", libopusDREDParseOutputMagic, out)
	if err != nil {
		return libopusDREDRecoveryWindowInfo{}, err
	}

	info := libopusDREDRecoveryWindowInfo{
		availableSamples:         int(reader.I32()),
		dredEndSamples:           int(reader.I32()),
		processRet:               int(reader.I32()),
		processStage:             int(reader.I32()),
		nbLatents:                int(reader.I32()),
		dredOffset:               int(reader.I32()),
		featuresPerFrame:         int(reader.I32()),
		neededFeatureFrames:      int(reader.I32()),
		featureOffsetBase:        int(reader.I32()),
		maxFeatureIndex:          int(reader.I32()),
		recoverableFeatureFrames: int(reader.I32()),
		missingPositiveFrames:    int(reader.I32()),
	}

	info.featureOffsets = make([]int, info.neededFeatureFrames)
	for i := range info.featureOffsets {
		info.featureOffsets[i] = int(reader.I32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDREDRecoveryWindowInfo{}, err
	}
	return info, nil
}
