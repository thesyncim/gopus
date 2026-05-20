package celt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	libopusCELTMathInputMagic  = "GCMI"
	libopusCELTMathOutputMagic = "GCMO"

	libopusCELTMathModeLog2 = uint32(0)
	libopusCELTMathModeExp2 = uint32(1)
)

var (
	libopusCELTMathHelperOnce sync.Once
	libopusCELTMathHelperPath string
	libopusCELTMathHelperErr  error
)

func buildLibopusCELTMathHelper() (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}
	repoRoot := filepath.Clean("..")
	refDir := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion)
	if _, err := os.Stat(filepath.Join(refDir, "config.h")); err != nil {
		libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{repoRoot})
	}
	srcPath := filepath.Join(repoRoot, "tools", "csrc", "libopus_celt_math_info.c")
	outDir := filepath.Join(os.TempDir(), "gopus_celt_test_helpers")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir helper dir: %w", err)
	}
	outPath := filepath.Join(outDir, fmt.Sprintf("gopus_libopus_celt_math_%s_%s", runtime.GOOS, runtime.GOARCH))
	if runtime.GOOS == "windows" {
		outPath += ".exe"
	}
	args := []string{
		"-std=c99",
		"-O2",
		"-DHAVE_CONFIG_H",
		"-I", refDir,
		"-I", filepath.Join(refDir, "include"),
		"-I", filepath.Join(refDir, "celt"),
		srcPath,
		"-lm",
		"-o", outPath,
	}
	cmd := exec.Command(ccPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build celt math helper: %w (%s)", err, bytes.TrimSpace(output))
	}
	return outPath, nil
}

func getLibopusCELTMathHelperPath() (string, error) {
	libopusCELTMathHelperOnce.Do(func() {
		libopusCELTMathHelperPath, libopusCELTMathHelperErr = buildLibopusCELTMathHelper()
	})
	if libopusCELTMathHelperErr != nil {
		return "", libopusCELTMathHelperErr
	}
	return libopusCELTMathHelperPath, nil
}

func probeLibopusCELTMath(mode uint32, samples []float32) ([]float32, error) {
	binPath, err := getLibopusCELTMathHelperPath()
	if err != nil {
		return nil, err
	}
	var payload bytes.Buffer
	payload.WriteString(libopusCELTMathInputMagic)
	for _, v := range []uint32{1, mode, uint32(len(samples))} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return nil, err
		}
	}
	for _, sample := range samples {
		if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(sample)); err != nil {
			return nil, err
		}
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(binPath)
	cmd.Stdin = &payload
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run celt math helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	data := stdout.Bytes()
	if len(data) < 12 || string(data[:4]) != libopusCELTMathOutputMagic {
		return nil, fmt.Errorf("unexpected celt math helper output")
	}
	count := int(binary.LittleEndian.Uint32(data[8:12]))
	if count != len(samples) {
		return nil, fmt.Errorf("helper count=%d want %d", count, len(samples))
	}
	if len(data) != 12+4*count {
		return nil, fmt.Errorf("helper output length=%d want %d", len(data), 12+4*count)
	}
	out := make([]float32, count)
	offset := 12
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
	}
	return out, nil
}

func TestCELTLog2MatchesLibopusFloatApprox(t *testing.T) {
	samples := []float32{
		math.SmallestNonzeroFloat32,
		1e-30, 1e-20, 1e-10, 1e-5, 0.03125,
		0.5, 0.75, 0.99999994, 1, 1.0000001,
		1.125, 1.25, 1.5, 1.875, 2, 3, 8, 1024,
	}
	for exp := int32(-12); exp <= 12; exp++ {
		for mant := uint32(0); mant < 8; mant++ {
			bits := uint32(exp+127)<<23 | mant<<20 | 0x12345
			samples = append(samples, math.Float32frombits(bits))
		}
	}
	want, err := probeLibopusCELTMath(libopusCELTMathModeLog2, samples)
	if err != nil {
		t.Skipf("libopus celt math helper unavailable: %v", err)
	}
	for i, sample := range samples {
		got := celtLog2(sample)
		if math.Float32bits(got) != math.Float32bits(want[i]) {
			t.Fatalf("celtLog2(%g)=%08x(%g) want %08x(%g)",
				sample,
				math.Float32bits(got), got,
				math.Float32bits(want[i]), want[i],
			)
		}
	}
}

func TestCELTExp2MatchesLibopusFloatApprox(t *testing.T) {
	samples := []float32{
		-60, -51, -50.5, -50, -24, -10,
		-1.75, -1.5, -1.25, -1, -0.75, -0.5, -0.25,
		0, 0.25, 0.5, 0.75, 1, 1.25, 2, 5, 10, 24,
	}
	for integer := int32(-12); integer <= 12; integer++ {
		for _, frac := range []float32{0, 0.0625, 0.125, 0.33325195, 0.5, 0.875, 0.99902344} {
			samples = append(samples, float32(integer)+frac)
		}
	}
	want, err := probeLibopusCELTMath(libopusCELTMathModeExp2, samples)
	if err != nil {
		t.Skipf("libopus celt math helper unavailable: %v", err)
	}
	for i, sample := range samples {
		got := celtExp2(sample)
		if math.Float32bits(got) != math.Float32bits(want[i]) {
			t.Fatalf("celtExp2(%g)=%08x(%g) want %08x(%g)",
				sample,
				math.Float32bits(got), got,
				math.Float32bits(want[i]), want[i],
			)
		}
	}
}
