//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"sync"
	"testing"
)

const (
	libopusFloatQuantInputMagic  = "GFQI"
	libopusFloatQuantOutputMagic = "GFQO"

	libopusFloatQuantModeFloat2Int16 = uint32(0)
)

var (
	libopusFloatQuantHelperOnce sync.Once
	libopusFloatQuantHelperPath string
	libopusFloatQuantHelperErr  error
)

func getLibopusFloatQuantHelperPath() (string, error) {
	libopusFloatQuantHelperOnce.Do(func() {
		libopusFloatQuantHelperPath, libopusFloatQuantHelperErr = buildLibopusDREDHelper("libopus_float_quant_info.c", "gopus_libopus_float_quant", true)
	})
	if libopusFloatQuantHelperErr != nil {
		return "", libopusFloatQuantHelperErr
	}
	return libopusFloatQuantHelperPath, nil
}

func probeLibopusFloatQuant(mode uint32, samples []float32) ([]int16, error) {
	binPath, err := getLibopusFloatQuantHelperPath()
	if err != nil {
		return nil, err
	}
	var payload bytes.Buffer
	payload.WriteString(libopusFloatQuantInputMagic)
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
		return nil, fmt.Errorf("run float quant helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	data := stdout.Bytes()
	if len(data) < 12 || string(data[:4]) != libopusFloatQuantOutputMagic {
		return nil, fmt.Errorf("unexpected float quant helper output")
	}
	count := int(binary.LittleEndian.Uint32(data[8:12]))
	if count != len(samples) {
		return nil, fmt.Errorf("helper count=%d want %d", count, len(samples))
	}
	if len(data) != 12+2*count {
		return nil, fmt.Errorf("helper output length=%d want %d", len(data), 12+2*count)
	}
	out := make([]int16, count)
	offset := 12
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(data[offset:]))
		offset += 2
	}
	return out, nil
}

func TestFloat32ToInt16MatchesLibopusFLOAT2INT16ExhaustiveGrid(t *testing.T) {
	samples := make([]float32, 0, 65536)
	for i := -32768; i <= 32767; i++ {
		samples = append(samples, float32(i)*(1.0/32768.0))
	}
	want, err := probeLibopusFloatQuant(libopusFloatQuantModeFloat2Int16, samples)
	if err != nil {
		t.Skipf("libopus float quant helper unavailable: %v", err)
	}
	for i, sample := range samples {
		if got := float32ToInt16(sample); got != want[i] {
			raw := i - 32768
			t.Fatalf("float32ToInt16(%d/32768)=%d want %d", raw, got, want[i])
		}
	}
}

func TestFloat32ToInt16MatchesLibopusFLOAT2INT16TiesAndClamps(t *testing.T) {
	samples := []float32{
		float32(-32769.0 / 32768.0),
		-1,
		float32(-32767.5 / 32768.0),
		float32(-3.5 / 32768.0),
		float32(-2.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		float32(2.5 / 32768.0),
		float32(3.5 / 32768.0),
		float32(32766.5 / 32768.0),
		float32(32767.5 / 32768.0),
		1,
		float32(32768.5 / 32768.0),
	}
	want, err := probeLibopusFloatQuant(libopusFloatQuantModeFloat2Int16, samples)
	if err != nil {
		t.Skipf("libopus float quant helper unavailable: %v", err)
	}
	for i, sample := range samples {
		if got := float32ToInt16(sample); got != want[i] {
			t.Fatalf("float32ToInt16(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}
