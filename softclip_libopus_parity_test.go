//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package gopus

import (
	"fmt"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSoftClipInputMagic  = "GSCI"
	libopusSoftClipOutputMagic = "GSCO"
)

var (
	libopusSoftClipHelperOnce sync.Once
	libopusSoftClipHelperPath string
	libopusSoftClipHelperErr  error
)

func getLibopusSoftClipHelperPath() (string, error) {
	libopusSoftClipHelperOnce.Do(func() {
		libopusSoftClipHelperPath, libopusSoftClipHelperErr = buildLibopusDREDHelper("libopus_softclip_info.c", "gopus_libopus_softclip", false)
	})
	if libopusSoftClipHelperErr != nil {
		return "", libopusSoftClipHelperErr
	}
	return libopusSoftClipHelperPath, nil
}

func probeLibopusSoftClip(n, channels int, samples, mem []float32) ([]float32, []float32, error) {
	binPath, err := getLibopusSoftClipHelperPath()
	if err != nil {
		return nil, nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSoftClipInputMagic, uint32(n), uint32(channels))
	for _, v := range mem {
		payload.Float32(v)
	}
	for _, v := range samples {
		payload.Float32(v)
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("run softclip helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("softclip", libopusSoftClipOutputMagic, data)
	if err != nil {
		return nil, nil, err
	}
	countN := int(reader.U32())
	countC := int(reader.U32())
	if countN != n || countC != channels {
		return nil, nil, fmt.Errorf("helper shape=%dx%d want %dx%d", countN, countC, n, channels)
	}
	total := countN * countC
	reader.ExpectRemaining(4*countC + 4*total)
	outMem := make([]float32, countC)
	for i := range outMem {
		outMem[i] = reader.Float32()
	}
	out := make([]float32, total)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	return out, outMem, nil
}

func TestOpusPCMSoftClipMatchesLibopus(t *testing.T) {
	tests := []struct {
		name     string
		n        int
		channels int
		mem      []float32
		samples  []float32
	}{
		{
			name:     "mono clipped segments",
			n:        8,
			channels: 1,
			mem:      []float32{0},
			samples:  []float32{1.25, 1.5, 1.8, 1.4, 0.8, -0.2, -1.2, -1.6},
		},
		{
			name:     "stereo independent channels",
			n:        6,
			channels: 2,
			mem:      []float32{0, 0},
			samples:  []float32{0.2, -1.8, 1.4, -1.2, 1.7, -0.4, 0.6, 0.3, -0.5, 1.1, -1.3, 1.9},
		},
		{
			name:     "carryover memory",
			n:        6,
			channels: 1,
			mem:      []float32{-0.2},
			samples:  []float32{0.8, 0.4, -0.2, 1.4, 1.1, 0.3},
		},
		{
			name:     "hard clamp domain",
			n:        5,
			channels: 1,
			mem:      []float32{0},
			samples:  []float32{2.5, -2.5, 3, -3, 0},
		},
		{
			name:     "stereo carryover only",
			n:        4,
			channels: 2,
			mem:      []float32{-0.1, 0.12},
			samples:  []float32{0.5, -0.5, 0.25, -0.25, -0.1, 0.1, 0, 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, wantMem, err := probeLibopusSoftClip(tc.n, tc.channels, tc.samples, tc.mem)
			if err != nil {
				t.Skipf("libopus softclip helper unavailable: %v", err)
			}
			got := append([]float32(nil), tc.samples...)
			gotMem := append([]float32(nil), tc.mem...)
			opusPCMSoftClip(got, tc.n, tc.channels, gotMem)
			assertFloat32BitsEqual(t, got, want, "pcm")
			assertFloat32BitsEqual(t, gotMem, wantMem, "mem")
		})
	}
}

func TestSoftClipAndFloat32ToInt16MatchesLibopus(t *testing.T) {
	n := 8
	channels := 2
	mem := []float32{-0.08, 0.11}
	src := []float32{
		1.3, -0.9,
		1.7, -1.8,
		0.9, -1.2,
		-0.1, 0.4,
		-1.4, 1.6,
		-1.9, 1.2,
		-0.6, 0.2,
		0.1, -0.1,
	}

	wantFloat, wantMem, err := probeLibopusSoftClip(n, channels, src, mem)
	if err != nil {
		t.Skipf("libopus softclip helper unavailable: %v", err)
	}
	want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeFloat2Int16, wantFloat)
	if err != nil {
		t.Skipf("libopus float quant helper unavailable: %v", err)
	}

	gotSrc := append([]float32(nil), src...)
	gotMem := append([]float32(nil), mem...)
	got := make([]int16, len(src))
	softClipAndFloat32ToInt16(got, gotSrc, n, channels, gotMem)

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%d want %d", i, got[i], want[i])
		}
	}
	assertFloat32BitsEqual(t, gotSrc, wantFloat, "softclipped pcm")
	assertFloat32BitsEqual(t, gotMem, wantMem, "mem")
}
