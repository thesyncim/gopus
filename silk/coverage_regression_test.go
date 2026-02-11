package silk

import (
	"math"
	"testing"
)

func TestComputeSubframeGainsFromResidual_EdgeCases(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	empty := enc.computeSubframeGainsFromResidual(nil, 4)
	if len(empty) != 4 {
		t.Fatalf("empty gains len=%d want 4", len(empty))
	}
	for i, g := range empty {
		if g != 1.0 {
			t.Fatalf("empty gains[%d]=%f want 1.0", i, g)
		}
	}

	pcm := make([]float32, 160)
	enc.lastTotalEnergy = 0
	enc.lastInvGain = 1
	enc.lastNumSamples = len(pcm)
	fallback := enc.computeSubframeGainsFromResidual(pcm, 4)
	for i, g := range fallback {
		if g != 1.0 {
			t.Fatalf("fallback gains[%d]=%f want 1.0", i, g)
		}
	}

	// Very small residual should clamp to the minimum gain.
	enc.lastTotalEnergy = 1.0
	enc.lastInvGain = 1e-12
	enc.lastNumSamples = len(pcm)
	minClamped := enc.computeSubframeGainsFromResidual(pcm, 4)
	for i, g := range minClamped {
		if g != 1.0 {
			t.Fatalf("minClamped gains[%d]=%f want 1.0", i, g)
		}
	}

	// Very large residual should clamp to the maximum gain.
	for i := range pcm {
		pcm[i] = 1.0
	}
	enc.lastTotalEnergy = 1e16
	enc.lastInvGain = 1.0
	enc.lastNumSamples = 1
	maxClamped := enc.computeSubframeGainsFromResidual(pcm, 4)
	foundMax := false
	for i, g := range maxClamped {
		if g < 1.0 || g > 32767.0 {
			t.Fatalf("maxClamped gains[%d]=%f out of [1,32767]", i, g)
		}
		if g == 32767.0 {
			foundMax = true
		}
	}
	if !foundMax {
		t.Fatal("expected at least one gain clamped at 32767.0")
	}
}

func TestDetectPitch_NoSignalAndShortInput(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	lags, lagIndex, contour := enc.detectPitch(nil, 4, 0, 0)
	if lags != nil || lagIndex != 0 || contour != 0 {
		t.Fatalf("nil input detectPitch returned lags=%v lag=%d contour=%d", lags, lagIndex, contour)
	}

	short := make([]float32, 16)
	for i := range short {
		short[i] = float32(math.Sin(float64(i) * 0.25))
	}
	lags, lagIndex, contour = enc.detectPitch(short, 4, 0, 1e9)
	if len(lags) != 4 {
		t.Fatalf("short input lags len=%d want 4", len(lags))
	}
	for i, v := range lags {
		if v != 0 {
			t.Fatalf("short input lags[%d]=%d want 0", i, v)
		}
	}
	if lagIndex != 0 || contour != 0 {
		t.Fatalf("short input lag=%d contour=%d want 0,0", lagIndex, contour)
	}

	silence := make([]float32, 640)
	lags, lagIndex, contour = enc.detectPitch(silence, 4, 0, 1e9)
	if len(lags) != 4 {
		t.Fatalf("silence lags len=%d want 4", len(lags))
	}
	for i, v := range lags {
		if v != 0 {
			t.Fatalf("silence lags[%d]=%d want 0", i, v)
		}
	}
	if lagIndex != 0 || contour != 0 {
		t.Fatalf("silence lag=%d contour=%d want 0,0", lagIndex, contour)
	}
}

func TestComputeLPCFromFrameStoresResidualState(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	cfg := GetBandwidthConfig(BandwidthWideband)
	frameSamples := cfg.SampleRate * 20 / 1000

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		tm := float64(i) / float64(cfg.SampleRate)
		pcm[i] = 0.4 * float32(math.Sin(2*math.Pi*300*tm))
	}

	_ = enc.computeLPCFromFrame(pcm)
	if enc.lastTotalEnergy <= 0 {
		t.Fatalf("lastTotalEnergy=%f want > 0", enc.lastTotalEnergy)
	}
	if enc.lastInvGain <= 0 {
		t.Fatalf("lastInvGain=%f want > 0", enc.lastInvGain)
	}
	if enc.lastNumSamples <= 0 {
		t.Fatalf("lastNumSamples=%d want > 0", enc.lastNumSamples)
	}
}
