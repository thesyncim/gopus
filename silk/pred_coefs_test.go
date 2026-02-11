package silk

import (
	"math"
	"testing"
)

func TestComputeMinInvGain(t *testing.T) {
	got := computeMinInvGain(0, 1.0, true)
	want := 1.0 / maxPredictionPowerGainAfterReset
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("first frame minInvGain: got %.9f want %.9f", got, want)
	}

	got = computeMinInvGain(0, 1.0, false)
	want = 1.0 / maxPredictionPowerGain
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("codingQuality=1 minInvGain: got %.9f want %.9f", got, want)
	}

	got = computeMinInvGain(0, 0.0, false)
	want = 4.0 / maxPredictionPowerGain
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("codingQuality=0 minInvGain: got %.9f want %.9f", got, want)
	}
}

func TestComputeLPCAndNLSFWithInterpRespectsComplexity(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	enc.SetComplexity(0)
	enc.MarkEncoded()

	cfg := GetBandwidthConfig(BandwidthWideband)
	numSubframes := maxNbSubfr
	subframeSamples := cfg.SubframeSamples
	totalLen := numSubframes * (subframeSamples + enc.lpcOrder)
	ltpRes := make([]float32, totalLen)

	minInvGainVal := computeMinInvGain(0, 1.0, false)
	_, _, interpIdx := enc.computeLPCAndNLSFWithInterp(ltpRes, numSubframes, subframeSamples, minInvGainVal)
	if interpIdx != 4 {
		t.Fatalf("expected interpIdx=4 when complexity<4, got %d", interpIdx)
	}
}
