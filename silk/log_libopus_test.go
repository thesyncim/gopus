package silk

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	libopusSILKLogInputMagic  = "GSLI"
	libopusSILKLogOutputMagic = "GSLO"

	libopusSILKLogModeLin2Log = uint32(0)
	libopusSILKLogModeLog2Lin = uint32(1)
)

var (
	libopusSILKLogHelperOnce sync.Once
	libopusSILKLogHelperPath string
	libopusSILKLogHelperErr  error
)

func buildLibopusSILKLogHelper() (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}
	repoRoot := filepath.Clean("..")
	refDir := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion)
	if _, err := os.Stat(filepath.Join(refDir, "config.h")); err != nil {
		libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{repoRoot})
	}
	srcPath := filepath.Join(repoRoot, "tools", "csrc", "libopus_silk_log_info.c")
	outDir := filepath.Join(os.TempDir(), "gopus_silk_test_helpers")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir helper dir: %w", err)
	}
	outPath := filepath.Join(outDir, fmt.Sprintf("gopus_libopus_silk_log_%s_%s", runtime.GOOS, runtime.GOARCH))
	if runtime.GOOS == "windows" {
		outPath += ".exe"
	}
	args := []string{
		"-std=c99",
		"-O2",
		"-DHAVE_CONFIG_H",
		"-I", refDir,
		"-I", filepath.Join(refDir, "include"),
		"-I", filepath.Join(refDir, "silk"),
		srcPath,
		filepath.Join(refDir, "silk", "lin2log.c"),
		filepath.Join(refDir, "silk", "log2lin.c"),
		"-lm",
		"-o", outPath,
	}
	cmd := exec.Command(ccPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build silk log helper: %w (%s)", err, bytes.TrimSpace(output))
	}
	return outPath, nil
}

func getLibopusSILKLogHelperPath() (string, error) {
	libopusSILKLogHelperOnce.Do(func() {
		libopusSILKLogHelperPath, libopusSILKLogHelperErr = buildLibopusSILKLogHelper()
	})
	if libopusSILKLogHelperErr != nil {
		return "", libopusSILKLogHelperErr
	}
	return libopusSILKLogHelperPath, nil
}

func probeLibopusSILKLog(mode uint32, samples []int32) ([]int32, error) {
	binPath, err := getLibopusSILKLogHelperPath()
	if err != nil {
		return nil, err
	}
	var payload bytes.Buffer
	payload.WriteString(libopusSILKLogInputMagic)
	for _, v := range []uint32{1, mode, uint32(len(samples))} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return nil, err
		}
	}
	for _, sample := range samples {
		if err := binary.Write(&payload, binary.LittleEndian, sample); err != nil {
			return nil, err
		}
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(binPath)
	cmd.Stdin = &payload
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run silk log helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	data := stdout.Bytes()
	if len(data) < 12 || string(data[:4]) != libopusSILKLogOutputMagic {
		return nil, fmt.Errorf("unexpected silk log helper output")
	}
	count := int(binary.LittleEndian.Uint32(data[8:12]))
	if count != len(samples) {
		return nil, fmt.Errorf("helper count=%d want %d", count, len(samples))
	}
	if len(data) != 12+4*count {
		return nil, fmt.Errorf("helper output length=%d want %d", len(data), 12+4*count)
	}
	out := make([]int32, count)
	offset := 12
	for i := range out {
		out[i] = int32(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
	}
	return out, nil
}

func TestSILKLin2LogMatchesLibopus(t *testing.T) {
	samples := []int32{1, 2, 3, 4, 5, 7, 8, 15, 16, 31, 32, 63, 64, 127, 128, 255, 256, 1024, 65535, 65536, 0x7fffffff}
	for shift := 1; shift < 31; shift++ {
		base := int32(1) << shift
		for delta := int32(-2); delta <= 2; delta++ {
			v := base + delta
			if v > 0 {
				samples = append(samples, v)
			}
		}
	}
	want, err := probeLibopusSILKLog(libopusSILKLogModeLin2Log, samples)
	if err != nil {
		t.Skipf("libopus silk log helper unavailable: %v", err)
	}
	for i, sample := range samples {
		if got := silkLin2Log(sample); got != want[i] {
			t.Fatalf("silkLin2Log(%d)=%d want %d", sample, got, want[i])
		}
	}
}

func TestSILKLog2LinMatchesLibopus(t *testing.T) {
	samples := []int32{-16, -1, 0, 1, 2, 63, 64, 65, 127, 128, 129, 2047, 2048, 2049, 3966, 3967, 4096}
	for x := int32(0); x <= 3967; x++ {
		samples = append(samples, x)
	}
	want, err := probeLibopusSILKLog(libopusSILKLogModeLog2Lin, samples)
	if err != nil {
		t.Skipf("libopus silk log helper unavailable: %v", err)
	}
	for i, sample := range samples {
		if got := silkLog2Lin(sample); got != want[i] {
			t.Fatalf("silkLog2Lin(%d)=%d want %d", sample, got, want[i])
		}
	}
}
