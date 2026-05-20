//go:build gopus_extra_controls
// +build gopus_extra_controls

package gopus

import "testing"

const libopusFloatQuantModeOSCEOutputScale = uint32(1)

func TestOSCEFloatToInt16MatchesLibopusOutputScaleExhaustiveGrid(t *testing.T) {
	samples := make([]float32, 0, 65536)
	for i := -32768; i <= 32767; i++ {
		samples = append(samples, float32(i)*(1.0/32768.0))
	}
	want, err := probeLibopusFloatQuant(libopusFloatQuantModeOSCEOutputScale, samples)
	if err != nil {
		t.Skipf("libopus float quant helper unavailable: %v", err)
	}
	for i, sample := range samples {
		if got := osceFloatToInt16(sample); got != want[i] {
			raw := i - 32768
			t.Fatalf("osceFloatToInt16(%d/32768)=%d want %d", raw, got, want[i])
		}
	}
}

func TestOSCEFloatToInt16MatchesLibopusOutputScaleTiesAndClamps(t *testing.T) {
	samples := []float32{
		float32(-32769.0 / 32768.0),
		-1,
		float32(-32767.5 / 32768.0),
		float32(-32766.5 / 32768.0),
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
	want, err := probeLibopusFloatQuant(libopusFloatQuantModeOSCEOutputScale, samples)
	if err != nil {
		t.Skipf("libopus float quant helper unavailable: %v", err)
	}
	for i, sample := range samples {
		if got := osceFloatToInt16(sample); got != want[i] {
			t.Fatalf("osceFloatToInt16(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}
