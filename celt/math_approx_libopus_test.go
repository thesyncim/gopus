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

	libopusCELTMathModeFracMul16       = uint32(2)
	libopusCELTMathModeBitexactCos     = uint32(3)
	libopusCELTMathModeBitexactLog2Tan = uint32(4)
	libopusCELTMathModeISqrt32         = uint32(5)
	libopusCELTMathModeUdiv            = uint32(6)
	libopusCELTMathModeSudiv           = uint32(7)
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
		filepath.Join(refDir, "celt", "mathops.c"),
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

func probeLibopusCELTMathWords(mode uint32, count int, words []uint32) ([]uint32, error) {
	binPath, err := getLibopusCELTMathHelperPath()
	if err != nil {
		return nil, err
	}
	var payload bytes.Buffer
	payload.WriteString(libopusCELTMathInputMagic)
	for _, v := range []uint32{1, mode, uint32(count)} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return nil, err
		}
	}
	for _, word := range words {
		if err := binary.Write(&payload, binary.LittleEndian, word); err != nil {
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
	gotCount := int(binary.LittleEndian.Uint32(data[8:12]))
	if gotCount != count {
		return nil, fmt.Errorf("helper count=%d want %d", gotCount, count)
	}
	if len(data) != 12+4*gotCount {
		return nil, fmt.Errorf("helper output length=%d want %d", len(data), 12+4*gotCount)
	}
	out := make([]uint32, gotCount)
	offset := 12
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(data[offset:])
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

func TestCELTBitexactCosMatchesLibopus(t *testing.T) {
	inputs := make([]uint32, 0, 16320-64+1)
	for x := uint32(64); x <= 16320; x++ {
		inputs = append(inputs, x)
	}
	want, err := probeLibopusCELTMathWords(libopusCELTMathModeBitexactCos, len(inputs), inputs)
	if err != nil {
		t.Skipf("libopus celt math helper unavailable: %v", err)
	}
	for i, x := range inputs {
		got := bitexactCos(int(x))
		if got != int(int32(want[i])) {
			t.Fatalf("bitexactCos(%d)=%d want %d", x, got, int32(want[i]))
		}
	}
}

func TestCELTBitexactLog2TanMatchesLibopus(t *testing.T) {
	type pair struct {
		isin int
		icos int
	}
	pairs := []pair{
		{isin: 32767, icos: 200},
		{isin: 30274, icos: 12540},
		{isin: 23171, icos: 23171},
		{isin: 200, icos: 32767},
		{isin: 12540, icos: 30274},
	}
	for theta := 64; theta <= 8192; theta++ {
		pairs = append(pairs, pair{
			isin: bitexactCos(theta),
			icos: bitexactCos(16384 - theta),
		})
	}
	words := make([]uint32, 0, 2*len(pairs))
	for _, p := range pairs {
		words = append(words, uint32(int32(p.isin)), uint32(int32(p.icos)))
	}
	want, err := probeLibopusCELTMathWords(libopusCELTMathModeBitexactLog2Tan, len(pairs), words)
	if err != nil {
		t.Skipf("libopus celt math helper unavailable: %v", err)
	}
	for i, p := range pairs {
		got := bitexactLog2tan(p.isin, p.icos)
		if got != int(int32(want[i])) {
			t.Fatalf("bitexactLog2tan(%d,%d)=%d want %d", p.isin, p.icos, got, int32(want[i]))
		}
	}
}

func TestCELTIntegerMathMatchesLibopus(t *testing.T) {
	t.Run("fracMul16", func(t *testing.T) {
		values := []int{-40000, -32768, -32767, -12345, -1, 0, 1, 12345, 32766, 32767, 32768, 40000}
		words := make([]uint32, 0, 2*len(values)*len(values))
		for _, a := range values {
			for _, b := range values {
				words = append(words, uint32(int32(a)), uint32(int32(b)))
			}
		}
		want, err := probeLibopusCELTMathWords(libopusCELTMathModeFracMul16, len(words)/2, words)
		if err != nil {
			t.Skipf("libopus celt math helper unavailable: %v", err)
		}
		idx := 0
		for _, a := range values {
			for _, b := range values {
				got := fracMul16(a, b)
				if got != int(int32(want[idx])) {
					t.Fatalf("fracMul16(%d,%d)=%d want %d", a, b, got, int32(want[idx]))
				}
				idx++
			}
		}
	})

	t.Run("isqrt32", func(t *testing.T) {
		inputs := []uint32{
			1, 2, 3, 4, 15, 16, 17,
			(1 << 16) - 1, 1 << 16, (1 << 16) + 1,
			(1 << 24) - 1, 1 << 24, (1 << 24) + 1,
			^uint32(0) - 2, ^uint32(0) - 1, ^uint32(0),
		}
		for i := uint32(1); i < 65536; i += 257 {
			inputs = append(inputs, i*i, i*i+1)
			if i > 0 {
				inputs = append(inputs, i*i-1)
			}
		}
		want, err := probeLibopusCELTMathWords(libopusCELTMathModeISqrt32, len(inputs), inputs)
		if err != nil {
			t.Skipf("libopus celt math helper unavailable: %v", err)
		}
		for i, x := range inputs {
			got := isqrt32(x)
			if got != want[i] {
				t.Fatalf("isqrt32(%d)=%d want %d", x, got, want[i])
			}
		}
	})

	t.Run("udiv", func(t *testing.T) {
		pairs := [][2]uint32{
			{0, 1},
			{1, 1},
			{2, 2},
			{3, 2},
			{255, 7},
			{256, 3},
			{65535, 257},
			{1 << 31, 3},
			{^uint32(0), 65535},
			{^uint32(0), 257},
		}
		words := make([]uint32, 0, 2*len(pairs))
		for _, p := range pairs {
			words = append(words, p[0], p[1])
		}
		want, err := probeLibopusCELTMathWords(libopusCELTMathModeUdiv, len(pairs), words)
		if err != nil {
			t.Skipf("libopus celt math helper unavailable: %v", err)
		}
		for i, p := range pairs {
			got := celtUdiv(int(p[0]), int(p[1]))
			if uint32(got) != want[i] {
				t.Fatalf("celtUdiv(%d,%d)=%d want %d", p[0], p[1], got, want[i])
			}
		}
	})

	t.Run("sudiv", func(t *testing.T) {
		pairs := [][2]int32{
			{-2147483647, 3},
			{-65536, 257},
			{-3, 2},
			{-1, 2},
			{0, 1},
			{1, 2},
			{3, 2},
			{2147483647, 65535},
		}
		words := make([]uint32, 0, 2*len(pairs))
		for _, p := range pairs {
			words = append(words, uint32(p[0]), uint32(p[1]))
		}
		want, err := probeLibopusCELTMathWords(libopusCELTMathModeSudiv, len(pairs), words)
		if err != nil {
			t.Skipf("libopus celt math helper unavailable: %v", err)
		}
		for i, p := range pairs {
			got := celtSudiv(int(p[0]), int(p[1]))
			if int32(got) != int32(want[i]) {
				t.Fatalf("celtSudiv(%d,%d)=%d want %d", p[0], p[1], got, int32(want[i]))
			}
		}
	})
}
