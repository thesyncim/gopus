//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package dred

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusDREDConvertInputMagic  = "GDCI"
	libopusDREDConvertOutputMagic = "GDCO"
)

var (
	libopusDREDConvertHelperOnce sync.Once
	libopusDREDConvertHelperPath string
	libopusDREDConvertHelperErr  error
)

func buildLibopusDREDHelper(sourceFile, outputBase string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	return libopustest.BuildDREDHelper(repoRoot, sourceFile, outputBase, true)
}

func getLibopusDREDConvertHelperPath() (string, error) {
	libopusDREDConvertHelperOnce.Do(func() {
		libopusDREDConvertHelperPath, libopusDREDConvertHelperErr = buildLibopusDREDHelper("libopus_dred_convert16k_info.c", "gopus_libopus_dred_convert16k")
	})
	if libopusDREDConvertHelperErr != nil {
		return "", libopusDREDConvertHelperErr
	}
	return libopusDREDConvertHelperPath, nil
}

func probeLibopusDREDConvert16k(sampleRate, channels int, mem [ResamplingOrder + 1]float32, input []float32) ([]float32, [ResamplingOrder + 1]float32, error) {
	binPath, err := getLibopusDREDConvertHelperPath()
	if err != nil {
		return nil, [ResamplingOrder + 1]float32{}, err
	}
	if channels != 1 && channels != 2 {
		return nil, [ResamplingOrder + 1]float32{}, fmt.Errorf("invalid channels")
	}
	if len(input) == 0 || len(input)%channels != 0 {
		return nil, [ResamplingOrder + 1]float32{}, fmt.Errorf("input must contain a positive whole number of channel frames")
	}

	frameSamples := len(input) / channels
	payload := libopustest.NewOraclePayload(libopusDREDConvertInputMagic, uint32(sampleRate), uint32(channels), uint32(frameSamples))
	for _, v := range mem {
		payload.Float32(v)
	}
	for _, v := range input {
		payload.Float32(v)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "dred convert16k", libopusDREDConvertOutputMagic)
	if err != nil {
		return nil, [ResamplingOrder + 1]float32{}, err
	}
	outLen := reader.Count(-1)
	reader.ExpectRemaining(4 * (outLen + ResamplingOrder + 1))
	output := make([]float32, outLen)
	for i := range output {
		output[i] = reader.Float32()
	}
	var nextMem [ResamplingOrder + 1]float32
	for i := range nextMem {
		nextMem[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, [ResamplingOrder + 1]float32{}, err
	}
	return output, nextMem, nil
}
