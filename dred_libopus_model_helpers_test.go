//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var (
	libopusPitchDNNModelBlobHelper libopustest.HelperCache
	libopusPLCModelBlobHelper      libopustest.HelperCache
	libopusFARGANModelBlobHelper   libopustest.HelperCache
)

func getLibopusPitchDNNModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusPitchDNNModelBlobHelper, "libopus_pitchdnn_model_blob.c", "gopus_libopus_pitchdnn_model_blob", true)
}

func getLibopusPLCModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusPLCModelBlobHelper, "libopus_plc_model_blob.c", "gopus_libopus_plc_model_blob", true)
}

func getLibopusFARGANModelBlobHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusFARGANModelBlobHelper, "libopus_fargan_model_blob.c", "gopus_libopus_fargan_model_blob", true)
}

func runModelBlobHelper(binPath string) ([]byte, error) {
	out, err := libopustest.RunHelper(binPath, nil)
	if err != nil {
		return nil, fmt.Errorf("run model blob helper: %w", err)
	}
	return out, nil
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
		libopustest.HelperUnavailable(t, "decoder neural model", err)
	}
	return blob
}
