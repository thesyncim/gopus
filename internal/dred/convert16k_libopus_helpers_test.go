//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package dred

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

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	libopusDREDConvertInputMagic  = "GDCI"
	libopusDREDConvertOutputMagic = "GDCO"
)

var (
	libopusDREDBuildOnce sync.Once
	libopusDREDRepoRoot  string
	libopusDREDSourceDir string
	libopusDREDBuildDir  string
	libopusDREDBuildErr  error

	libopusDREDConvertHelperOnce sync.Once
	libopusDREDConvertHelperPath string
	libopusDREDConvertHelperErr  error
)

func ensureLibopusDREDBuild() (sourceDir, buildDir string, err error) {
	libopusDREDBuildOnce.Do(func() {
		repoRoot, err := os.Getwd()
		if err != nil {
			libopusDREDBuildErr = fmt.Errorf("getwd: %w", err)
			return
		}
		repoRoot = filepath.Clean(filepath.Join(repoRoot, "..", ".."))
		libopusDREDRepoRoot = repoRoot
		referenceDir := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion)
		sourceDir = filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+"-dredsrc-clean")
		buildDir = filepath.Join(repoRoot, "tmp_check", "build-opus-dred")
		libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
		if _, err := os.Stat(libopusStatic); err == nil {
			libopusDREDSourceDir = sourceDir
			libopusDREDBuildDir = buildDir
			return
		}

		if _, err := os.Stat(filepath.Join(sourceDir, "configure")); err != nil {
			libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{repoRoot})
			if _, refErr := os.Stat(filepath.Join(referenceDir, "configure")); refErr == nil {
				sourceDir = referenceDir
			} else {
				tarball := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+".tar.gz")
				if _, err := os.Stat(tarball); err != nil {
					libopusDREDBuildErr = fmt.Errorf("libopus tarball not found and no prepared source tree present: %w", err)
					return
				}
				if err := os.RemoveAll(sourceDir); err != nil {
					libopusDREDBuildErr = fmt.Errorf("remove stale libopus source dir: %w", err)
					return
				}
				if err := os.MkdirAll(sourceDir, 0o755); err != nil {
					libopusDREDBuildErr = fmt.Errorf("mkdir libopus source dir: %w", err)
					return
				}
				cmd := exec.Command("tar", "-xzf", tarball, "-C", sourceDir, "--strip-components=1")
				if output, err := cmd.CombinedOutput(); err != nil {
					libopusDREDBuildErr = fmt.Errorf("extract libopus source: %w (%s)", err, bytes.TrimSpace(output))
					return
				}
			}
		}
		if err := os.MkdirAll(buildDir, 0o755); err != nil {
			libopusDREDBuildErr = fmt.Errorf("mkdir libopus build dir: %w", err)
			return
		}
		if _, err := os.Stat(filepath.Join(buildDir, "Makefile")); err != nil {
			cmd := exec.Command(filepath.Join(sourceDir, "configure"),
				"--enable-static",
				"--disable-shared",
				"--disable-extra-programs",
				"--enable-dred",
			)
			cmd.Dir = buildDir
			if output, err := cmd.CombinedOutput(); err != nil {
				libopusDREDBuildErr = fmt.Errorf("configure libopus build: %w (%s)", err, bytes.TrimSpace(output))
				return
			}
		}
		makeCmd := exec.Command("make", fmt.Sprintf("-j%d", max(1, runtime.NumCPU())))
		makeCmd.Dir = buildDir
		if output, err := makeCmd.CombinedOutput(); err != nil {
			libopusDREDBuildErr = fmt.Errorf("build libopus: %w (%s)", err, bytes.TrimSpace(output))
			return
		}
		libopusDREDSourceDir = sourceDir
		libopusDREDBuildDir = buildDir
	})
	if libopusDREDBuildErr != nil {
		return "", "", libopusDREDBuildErr
	}
	return libopusDREDSourceDir, libopusDREDBuildDir, nil
}

func buildLibopusDREDHelper(sourceFile, outputBase string) (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}
	sourceDir, buildDir, err := ensureLibopusDREDBuild()
	if err != nil {
		return "", err
	}
	srcPath := filepath.Join(libopusDREDRepoRoot, "tools", "csrc", sourceFile)
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("dred helper source not found: %w", err)
	}
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	outPath := filepath.Join(buildDir, fmt.Sprintf("%s_%s_%s", outputBase, runtime.GOOS, runtime.GOARCH))
	if runtime.GOOS == "windows" {
		outPath += ".exe"
	}
	args := []string{
		"-std=c99",
		"-O2",
		"-I", filepath.Join(sourceDir, "include"),
		"-I", sourceDir,
		"-I", filepath.Join(sourceDir, "celt"),
		"-I", filepath.Join(sourceDir, "dnn"),
		srcPath,
		libopusStatic,
		"-lm",
		"-o",
		outPath,
	}
	cmd := exec.Command(ccPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build dred helper %s: %w (%s)", sourceFile, err, bytes.TrimSpace(output))
	}
	return outPath, nil
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
	var payload bytes.Buffer
	payload.WriteString(libopusDREDConvertInputMagic)
	for _, v := range []uint32{1, uint32(sampleRate), uint32(channels), uint32(frameSamples)} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return nil, [ResamplingOrder + 1]float32{}, fmt.Errorf("encode convert header: %w", err)
		}
	}
	writeBits := func(values []float32) error {
		for _, v := range values {
			if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(v)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeBits(mem[:]); err != nil {
		return nil, [ResamplingOrder + 1]float32{}, fmt.Errorf("encode convert mem: %w", err)
	}
	if err := writeBits(input); err != nil {
		return nil, [ResamplingOrder + 1]float32{}, fmt.Errorf("encode convert input: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, [ResamplingOrder + 1]float32{}, fmt.Errorf("run convert helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	data := stdout.Bytes()
	if len(data) < 12 || string(data[:4]) != libopusDREDConvertOutputMagic {
		return nil, [ResamplingOrder + 1]float32{}, fmt.Errorf("unexpected convert helper output")
	}
	outLen := int(binary.LittleEndian.Uint32(data[8:12]))
	offset := 12
	readBits := func(count int) ([]float32, error) {
		values := make([]float32, count)
		for i := 0; i < count; i++ {
			if len(data) < offset+4 {
				return nil, fmt.Errorf("truncated convert helper output")
			}
			values[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
			offset += 4
		}
		return values, nil
	}
	output, err := readBits(outLen)
	if err != nil {
		return nil, [ResamplingOrder + 1]float32{}, err
	}
	nextMemBits, err := readBits(ResamplingOrder + 1)
	if err != nil {
		return nil, [ResamplingOrder + 1]float32{}, err
	}
	var nextMem [ResamplingOrder + 1]float32
	copy(nextMem[:], nextMemBits)
	return output, nextMem, nil
}
