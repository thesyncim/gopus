//go:build gopus_extra_controls
// +build gopus_extra_controls

package multistream

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func repoRootForMultistreamFloatQuantTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, ".."))
}

func TestStreamOSCEFloatToInt16MatchesLibopusOutputScaleCGrid(t *testing.T) {
	samples := make([]float32, 0, 2*65540)
	for raw := -32770; raw <= 32769; raw++ {
		samples = append(samples, float32(raw)*(1.0/32768.0))
		samples = append(samples, float32(float64(raw)+0.5)*(1.0/32768.0))
	}
	want, err := libopustest.ProbeFloatQuant(repoRootForMultistreamFloatQuantTest(t), libopustest.FloatQuantModeOSCEOutputScale, samples)
	if err != nil {
		t.Skipf("libopus float quant helper unavailable: %v", err)
	}
	for i, sample := range samples {
		if got := streamOSCEFloatToInt16(sample); got != want[i] {
			t.Fatalf("streamOSCEFloatToInt16(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}
