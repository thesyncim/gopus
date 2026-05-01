//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import (
	"bytes"
	"fmt"
	"os/exec"
	"sync"
	"testing"
)

var (
	libopusPitchDNNModelBlobHelperOnce sync.Once
	libopusPitchDNNModelBlobHelperPath string
	libopusPitchDNNModelBlobHelperErr  error

	libopusPLCModelBlobHelperOnce sync.Once
	libopusPLCModelBlobHelperPath string
	libopusPLCModelBlobHelperErr  error

	libopusFARGANModelBlobHelperOnce sync.Once
	libopusFARGANModelBlobHelperPath string
	libopusFARGANModelBlobHelperErr  error
)

func getLibopusPitchDNNModelBlobHelperPath() (string, error) {
	libopusPitchDNNModelBlobHelperOnce.Do(func() {
		libopusPitchDNNModelBlobHelperPath, libopusPitchDNNModelBlobHelperErr = buildLibopusDREDHelper("libopus_pitchdnn_model_blob.c", "gopus_libopus_pitchdnn_model_blob", true)
	})
	if libopusPitchDNNModelBlobHelperErr != nil {
		return "", libopusPitchDNNModelBlobHelperErr
	}
	return libopusPitchDNNModelBlobHelperPath, nil
}

func getLibopusPLCModelBlobHelperPath() (string, error) {
	libopusPLCModelBlobHelperOnce.Do(func() {
		libopusPLCModelBlobHelperPath, libopusPLCModelBlobHelperErr = buildLibopusDREDHelper("libopus_plc_model_blob.c", "gopus_libopus_plc_model_blob", true)
	})
	if libopusPLCModelBlobHelperErr != nil {
		return "", libopusPLCModelBlobHelperErr
	}
	return libopusPLCModelBlobHelperPath, nil
}

func getLibopusFARGANModelBlobHelperPath() (string, error) {
	libopusFARGANModelBlobHelperOnce.Do(func() {
		libopusFARGANModelBlobHelperPath, libopusFARGANModelBlobHelperErr = buildLibopusDREDHelper("libopus_fargan_model_blob.c", "gopus_libopus_fargan_model_blob", true)
	})
	if libopusFARGANModelBlobHelperErr != nil {
		return "", libopusFARGANModelBlobHelperErr
	}
	return libopusFARGANModelBlobHelperPath, nil
}

func runModelBlobHelper(binPath string) ([]byte, error) {
	cmd := exec.Command(binPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run model blob helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

func probeLibopusDecoderNeuralModelBlob() ([]byte, error) {
	pitchPath, err := getLibopusPitchDNNModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	plcPath, err := getLibopusPLCModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	farganPath, err := getLibopusFARGANModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	pitchBlob, err := runModelBlobHelper(pitchPath)
	if err != nil {
		return nil, err
	}
	plcBlob, err := runModelBlobHelper(plcPath)
	if err != nil {
		return nil, err
	}
	farganBlob, err := runModelBlobHelper(farganPath)
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(plcBlob)+len(farganBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, plcBlob...)
	blob = append(blob, farganBlob...)
	return blob, nil
}

func requireLibopusDecoderNeuralModelBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeLibopusDecoderNeuralModelBlob()
	if err != nil {
		t.Skipf("libopus decoder neural model helper unavailable: %v", err)
	}
	return blob
}
