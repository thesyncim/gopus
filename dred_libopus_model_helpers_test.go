//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import (
	"bytes"
	"fmt"
	"os/exec"
	"sync"
)

var (
	libopusPitchDNNModelBlobHelperOnce sync.Once
	libopusPitchDNNModelBlobHelperPath string
	libopusPitchDNNModelBlobHelperErr  error
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
