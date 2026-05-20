//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package celt

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func probeLibopusCELTFloatQuant(t *testing.T, mode uint32, samples []float32) []int16 {
	t.Helper()
	want, err := libopustest.ProbeFloatQuant(mode, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float quant", err)
	}
	return want
}

func celtPCMGrid() []float32 {
	samples := make([]float32, 0, 2*65540)
	for raw := -32770; raw <= 32769; raw++ {
		samples = append(samples, float32(raw)*(1.0/32768.0))
		samples = append(samples, float32(float64(raw)+0.5)*(1.0/32768.0))
	}
	return samples
}

func TestFARGANPCM16GridSampleMatchesCGrid(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := celtPCMGrid()
	want := probeLibopusCELTFloatQuant(t, libopustest.FloatQuantModeFARGANSynthInt, samples)
	for i, sample := range samples {
		got := quantizedFARGANPCM16GridSample(sample)
		wantSample := float32(want[i]) * (1.0 / 32768.0)
		if got != wantSample {
			t.Fatalf("quantizedFARGANPCM16GridSample(%0.10g)=%0.10g want %0.10g", sample, got, wantSample)
		}
	}
}

func TestQuantizePLCPCM16kFrameMatchesCFARGANGrid(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := celtPCMGrid()
	want := probeLibopusCELTFloatQuant(t, libopustest.FloatQuantModeFARGANSynthInt, samples)
	got := append([]float32(nil), samples...)
	quantizePLCPCM16kFrame(got)
	for i := range got {
		wantSample := float32(want[i]) * (1.0 / 32768.0)
		if got[i] != wantSample {
			t.Fatalf("frame[%d]=%0.10g want %0.10g", i, got[i], wantSample)
		}
	}
}

func TestQuantizeRawPCM16LikeInt16MatchesCRawGrid(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := make([]float32, 0, 2*65540)
	for raw := -32770; raw <= 32769; raw++ {
		samples = append(samples, float32(raw))
		samples = append(samples, float32(raw)+0.5)
	}
	want := probeLibopusCELTFloatQuant(t, libopustest.FloatQuantModeCELTRaw32767Round, samples)
	for i, sample := range samples {
		if got := quantizeRawPCM16LikeInt16(sample); got != want[i] {
			t.Fatalf("quantizeRawPCM16LikeInt16(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}
