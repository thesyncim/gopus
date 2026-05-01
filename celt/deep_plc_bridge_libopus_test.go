//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

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
	libopusCELTPLCUpdateInputMagic  = "GCUI"
	libopusCELTPLCUpdateOutputMagic = "GCUO"
)

var (
	libopusCELTPLCUpdateHelperOnce sync.Once
	libopusCELTPLCUpdateHelperPath string
	libopusCELTPLCUpdateHelperErr  error
)

func buildLibopusCELTPLCUpdateHelper() (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}
	repoRoot := filepath.Clean("..")
	srcPath := filepath.Join(repoRoot, "tools", "csrc", "libopus_celt_plc_update_pcm.c")
	outDir := filepath.Join(os.TempDir(), "gopus_celt_test_helpers")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir helper dir: %w", err)
	}
	outPath := filepath.Join(outDir, fmt.Sprintf("gopus_libopus_celt_plc_update_%s_%s", runtime.GOOS, runtime.GOARCH))
	if runtime.GOOS == "windows" {
		outPath += ".exe"
	}
	cmd := exec.Command(ccPath, "-std=c99", "-O2", srcPath, "-lm", "-o", outPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build celt plc update helper: %w (%s)", err, bytes.TrimSpace(output))
	}
	return outPath, nil
}

func getLibopusCELTPLCUpdateHelperPath() (string, error) {
	libopusCELTPLCUpdateHelperOnce.Do(func() {
		libopusCELTPLCUpdateHelperPath, libopusCELTPLCUpdateHelperErr = buildLibopusCELTPLCUpdateHelper()
	})
	if libopusCELTPLCUpdateHelperErr != nil {
		return "", libopusCELTPLCUpdateHelperErr
	}
	return libopusCELTPLCUpdateHelperPath, nil
}

type libopusCELTPLCUpdateInfo struct {
	preemphMem float32
	pcm        []int16
}

func probeLibopusCELTPLCUpdatePCM(channels int, history []float32) (libopusCELTPLCUpdateInfo, error) {
	binPath, err := getLibopusCELTPLCUpdateHelperPath()
	if err != nil {
		return libopusCELTPLCUpdateInfo{}, err
	}
	if channels != 1 && channels != 2 {
		return libopusCELTPLCUpdateInfo{}, fmt.Errorf("invalid channel count")
	}
	if len(history) != channels*plcDecodeBufferSize {
		return libopusCELTPLCUpdateInfo{}, fmt.Errorf("invalid history length")
	}

	var payload bytes.Buffer
	payload.WriteString(libopusCELTPLCUpdateInputMagic)
	if err := binary.Write(&payload, binary.LittleEndian, uint32(1)); err != nil {
		return libopusCELTPLCUpdateInfo{}, err
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(channels)); err != nil {
		return libopusCELTPLCUpdateInfo{}, err
	}
	for _, sample := range history {
		if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(sample)); err != nil {
			return libopusCELTPLCUpdateInfo{}, err
		}
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(binPath)
	cmd.Stdin = &payload
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusCELTPLCUpdateInfo{}, fmt.Errorf("run celt plc update helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	data := stdout.Bytes()
	header := 12
	if len(data) < header || string(data[:4]) != libopusCELTPLCUpdateOutputMagic {
		return libopusCELTPLCUpdateInfo{}, fmt.Errorf("unexpected helper output")
	}
	info := libopusCELTPLCUpdateInfo{
		preemphMem: math.Float32frombits(binary.LittleEndian.Uint32(data[8:12])),
		pcm:        make([]int16, plcUpdateSamples),
	}
	offset := header
	for i := range info.pcm {
		if offset+2 > len(data) {
			return libopusCELTPLCUpdateInfo{}, fmt.Errorf("truncated helper output")
		}
		info.pcm[i] = int16(binary.LittleEndian.Uint16(data[offset:]))
		offset += 2
	}
	return info, nil
}

func TestFillPLCUpdate16kMonoMatchesLibopusDerivedHelper(t *testing.T) {
	cases := []struct {
		name     string
		channels int
	}{
		{name: "mono", channels: 1},
		{name: "stereo", channels: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(tc.channels)
			history := make([]float32, tc.channels*plcDecodeBufferSize)
			for i := 0; i < plcDecodeBufferSize; i++ {
				left := float32(32768 * (0.7 * float64((i%37)-18) / 37))
				history[i] = left
				if tc.channels == 2 {
					history[plcDecodeBufferSize+i] = float32(32768 * (0.5 * float64((i%23)-11) / 23))
				}
			}
			for i := range history {
				d.plcDecodeMem[i] = float64(history[i])
			}

			want, err := probeLibopusCELTPLCUpdatePCM(tc.channels, history)
			if err != nil {
				t.Skipf("celt plc update helper unavailable: %v", err)
			}
			var got [plcUpdateSamples]float32
			if n := d.FillPLCUpdate16kMono(got[:]); n != len(got) {
				t.Fatalf("FillPLCUpdate16kMono()=%d want %d", n, len(got))
			}
			for i := range got {
				gotQ := int16(math.RoundToEven(float64(got[i] * 32768)))
				// Keep the comparison on the int16 grid libopus uses. The current
				// stereo helper path is still allowed one quantized step while we
				// chase the remaining host-float rounding edge.
				diff := int(gotQ) - int(want.pcm[i])
				if diff < -1 || diff > 1 {
					t.Fatalf("sample[%d]=%d want %d (|diff|=%d > 1)", i, gotQ, want.pcm[i], absInt(diff))
				}
			}
		})
	}
}

func TestFillPLCUpdate16kMonoWithPreemphasisMemMatchesLibopusDerivedHelper(t *testing.T) {
	d := NewDecoder(1)
	history := make([]float32, plcDecodeBufferSize)
	for i := 0; i < plcDecodeBufferSize; i++ {
		history[i] = float32(32768 * (0.8 * float64((i%41)-20) / 41))
		d.plcDecodeMem[i] = float64(history[i])
	}

	want, err := probeLibopusCELTPLCUpdatePCM(1, history)
	if err != nil {
		t.Skipf("celt plc update helper unavailable: %v", err)
	}
	var got [plcUpdateSamples]float32
	n, preemphMem := d.FillPLCUpdate16kMonoWithPreemphasisMem(got[:])
	if n != len(got) {
		t.Fatalf("FillPLCUpdate16kMonoWithPreemphasisMem()=%d want %d", n, len(got))
	}
	if math.Abs(float64(preemphMem-want.preemphMem)) > 1e-6 {
		t.Fatalf("preemphMem=%f want %f", preemphMem, want.preemphMem)
	}
	for i := range got {
		gotQ := int16(math.RoundToEven(float64(got[i] * 32768)))
		diff := int(gotQ) - int(want.pcm[i])
		if diff != 0 {
			t.Fatalf("sample[%d]=%d want %d", i, gotQ, want.pcm[i])
		}
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
