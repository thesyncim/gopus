package qualitycompare

import (
	"math"
	"testing"
)

func TestCodedTierSelection(t *testing.T) {
	cases := []struct {
		name    string
		profile SignalProfile
		want    MetricTier
	}{
		{"48k mono 10ms coded", CodedProfile(48000, 1, 480), TierOpusCompare},
		{"48k stereo 10ms coded", CodedProfile(48000, 2, 960), TierOpusCompare},
		{"48k mono just under 10ms", CodedProfile(48000, 1, 479), TierWaveform},
		{"16k mono long", CodedProfile(16000, 1, 16000), TierWaveform},
		{"24k stereo long", CodedProfile(24000, 2, 48000), TierWaveform},
		{"48k mono long", CodedProfile(48000, 1, 48000), TierOpusCompare},
		{"48k mono mostly concealed", SignalProfile{SampleRate: 48000, Channels: 1, TotalSamples: 9600, CodedSamples: 100}, TierWaveform},
	}
	for _, tc := range cases {
		if got := tc.profile.codedTier(); got != tc.want {
			t.Errorf("%s: codedTier()=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestBarForAnchoring(t *testing.T) {
	if got := barFor(TierOpusCompare, IntentNearExact); got.MinQ != QualityBarNearExact.MinQ || got.MinCorr != QualityBarNearExact.MinCorr {
		t.Errorf("opus_compare/near-exact should map to QualityBarNearExact, got %+v", got)
	}
	if got := barFor(TierOpusCompare, IntentRFCConformance); got.MinQ != QualityBarRFC.MinQ {
		t.Errorf("opus_compare/RFC should map to QualityBarRFC, got %+v", got)
	}
	if got := barFor(TierWaveform, IntentNearExact); !math.IsInf(got.MinQ, -1) || got.MinCorr != 0.997 {
		t.Errorf("waveform/near-exact should not gate Q and require corr>=0.997, got %+v", got)
	}
	if got := barFor(TierWaveform, IntentRFCConformance); got.MinCorr != 0.985 {
		t.Errorf("waveform/RFC should require corr>=0.985, got %+v", got)
	}
}

// sine fills interleaved PCM with a per-channel tone, deterministic.
func sine(samplesPerChan, channels int, freq float64) []float32 {
	out := make([]float32, samplesPerChan*channels)
	for i := 0; i < samplesPerChan; i++ {
		v := float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/48000.0))
		for c := 0; c < channels; c++ {
			out[i*channels+c] = v
		}
	}
	return out
}

func TestAssertParityWaveformIdenticalPasses(t *testing.T) {
	// Sub-48k: waveform tier, pure-Go, no opus_compare binary needed.
	x := sine(8000, 1, 440)
	v := AssertParity(t, x, append([]float32(nil), x...), CodedProfile(16000, 1, len(x)), IntentNearExact, "16k identical")
	if len(v.Regions) != 1 || v.Regions[0].Tier != TierWaveform {
		t.Fatalf("want 1 waveform region, got %+v", v.Regions)
	}
	if v.Regions[0].Cmp.Corr < 0.999 {
		t.Fatalf("identical signal corr=%.6f", v.Regions[0].Cmp.Corr)
	}
}

func TestAssertParitySplitsCodedAndConcealed(t *testing.T) {
	// 16k (waveform tier for both regions, hermetic) stream: first half coded,
	// second half "concealment". Identical reference -> both regions pass, and
	// the verdict reports a coded and a concealed region.
	x := sine(8000, 2, 330)
	coded := len(x) / 2
	v := AssertParity(t, x, append([]float32(nil), x...),
		SignalProfile{SampleRate: 16000, Channels: 2, TotalSamples: len(x), CodedSamples: coded},
		IntentNearExact, "16k split")
	if len(v.Regions) != 2 {
		t.Fatalf("want coded+concealed regions, got %d: %+v", len(v.Regions), v.Regions)
	}
	if v.Regions[0].Name != "coded" || v.Regions[1].Name != "concealed" {
		t.Fatalf("region names: %s, %s", v.Regions[0].Name, v.Regions[1].Name)
	}
	for _, r := range v.Regions {
		if r.Tier != TierWaveform {
			t.Errorf("region %s tier=%v want waveform", r.Name, r.Tier)
		}
	}
}

// TestAssertParityConcealedTailNotScoredByQ proves the core safety property: on a
// 48 kHz stream whose concealed tail diverges enough to tank opus_compare Q, the
// coded prefix is scored by Q and the concealed tail by waveform corr/RMS, so a
// valid match is not falsely failed. Requires the opus_compare binary; skips if
// unavailable.
func TestAssertParityConcealedTailNotScoredByQ(t *testing.T) {
	coded := sine(4800, 1, 600) // 100 ms coded @ 48k
	if _, err := CompareDecodedFloat32(coded, coded, 48000, 1, 0); err != nil {
		t.Skipf("opus_compare unavailable: %v", err)
	}
	// Concealed tail: a near-match (within the waveform near-exact bar) that
	// opus_compare's psychoacoustic model would nonetheless score poorly if it
	// were applied to extrapolated content.
	tail := sine(4800, 1, 600)
	tailRef := make([]float32, len(tail))
	for i := range tail {
		tailRef[i] = tail[i] * 1.005 // <0.5% level offset: corr~1, rms~1.005
	}
	cand := append(append([]float32(nil), coded...), tail...)
	ref := append(append([]float32(nil), coded...), tailRef...)
	v := AssertParity(t, cand, ref,
		SignalProfile{SampleRate: 48000, Channels: 1, TotalSamples: len(cand), CodedSamples: len(coded)},
		IntentNearExact, "48k coded+concealed")
	if len(v.Regions) != 2 {
		t.Fatalf("want 2 regions, got %+v", v.Regions)
	}
	if v.Regions[0].Tier != TierOpusCompare {
		t.Errorf("coded region tier=%v want opus_compare", v.Regions[0].Tier)
	}
	if v.Regions[1].Tier != TierWaveform {
		t.Errorf("concealed region tier=%v want waveform", v.Regions[1].Tier)
	}
}
