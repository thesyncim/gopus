//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package dred

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
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
