package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type exportedDNN struct {
	EncoderPath string
	DecoderPath string
}

func exportDNNBlobs(outDir string) (exportedDNN, error) {
	if outDir == "" {
		outDir = "dnn"
	}
	root, err := repoRoot()
	if err != nil {
		return exportedDNN{}, err
	}
	buildDir := filepath.Join(root, "tmp_check", fmt.Sprintf("build-opus-dred-scalar-%s-%s", runtime.GOOS, runtime.GOARCH))
	suffix := fmt.Sprintf("_%s_%s", runtime.GOOS, runtime.GOARCH)
	helpers := map[string]string{
		"pitch":       filepath.Join(buildDir, "gopus_libopus_pitchdnn_model_blob"+suffix),
		"dredEncoder": filepath.Join(buildDir, "gopus_libopus_dred_encoder_model_blob"+suffix),
		"plc":         filepath.Join(buildDir, "gopus_libopus_plc_model_blob"+suffix),
		"fargan":      filepath.Join(buildDir, "gopus_libopus_fargan_model_blob"+suffix),
		"dredDecoder": filepath.Join(buildDir, "gopus_libopus_dred_model_blob"+suffix),
	}
	for name, path := range helpers {
		if _, err := os.Stat(path); err != nil {
			return exportedDNN{}, fmt.Errorf("missing %s helper %s; run make test-dnn-blob-parity or make test-dred-tag first", name, path)
		}
	}

	pitch, err := runBlobHelper(helpers["pitch"])
	if err != nil {
		return exportedDNN{}, err
	}
	dredEnc, err := runBlobHelper(helpers["dredEncoder"])
	if err != nil {
		return exportedDNN{}, err
	}
	plc, err := runBlobHelper(helpers["plc"])
	if err != nil {
		return exportedDNN{}, err
	}
	fargan, err := runBlobHelper(helpers["fargan"])
	if err != nil {
		return exportedDNN{}, err
	}
	dredDec, err := runBlobHelper(helpers["dredDecoder"])
	if err != nil {
		return exportedDNN{}, err
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return exportedDNN{}, err
	}
	exported := exportedDNN{
		EncoderPath: filepath.Join(outDir, "encoder-dred.blob"),
		DecoderPath: filepath.Join(outDir, "decoder-dred.blob"),
	}
	encoderBlob := append(append([]byte(nil), pitch...), dredEnc...)
	decoderBlob := append(append(append(append([]byte(nil), pitch...), plc...), fargan...), dredDec...)
	if err := os.WriteFile(exported.EncoderPath, encoderBlob, 0o644); err != nil {
		return exportedDNN{}, err
	}
	if err := os.WriteFile(exported.DecoderPath, decoderBlob, 0o644); err != nil {
		return exportedDNN{}, err
	}
	return exported, nil
}

func runBlobHelper(path string) ([]byte, error) {
	cmd := exec.Command(path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w %s", path, err, stderr.String())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s produced an empty blob", path)
	}
	return out, nil
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
			if err == nil && bytes.Contains(data, []byte("module github.com/thesyncim/gopus\n")) {
				return dir, nil
			}
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return "", fmt.Errorf("could not find repository root")
}
