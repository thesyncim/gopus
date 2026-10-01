package qualitycompare

import (
	"errors"
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
	for i := range samplesPerChan {
		v := float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/48000.0))
		for c := range channels {
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

func TestValidateParityInputsRejectsInvalidPCMAndProfiles(t *testing.T) {
	valid := []float32{0.1, -0.2, 0.3, -0.4}
	base := SignalProfile{SampleRate: 16000, Channels: 2, TotalSamples: len(valid), CodedSamples: 2}
	cases := []struct {
		name      string
		candidate []float32
		reference []float32
		profile   SignalProfile
		intent    ParityIntent
	}{
		{name: "empty PCM", candidate: nil, reference: nil, profile: base, intent: IntentNearExact},
		{name: "unequal PCM lengths", candidate: valid, reference: valid[:2], profile: base, intent: IntentNearExact},
		{name: "total samples too short", candidate: valid, reference: valid, profile: SignalProfile{SampleRate: 16000, Channels: 2, TotalSamples: 2, CodedSamples: 2}, intent: IntentNearExact},
		{name: "total samples too long", candidate: valid, reference: valid, profile: SignalProfile{SampleRate: 16000, Channels: 2, TotalSamples: 6, CodedSamples: 2}, intent: IntentNearExact},
		{name: "negative coded samples", candidate: valid, reference: valid, profile: SignalProfile{SampleRate: 16000, Channels: 2, TotalSamples: 4, CodedSamples: -2}, intent: IntentNearExact},
		{name: "coded samples exceed total", candidate: valid, reference: valid, profile: SignalProfile{SampleRate: 16000, Channels: 2, TotalSamples: 4, CodedSamples: 6}, intent: IntentNearExact},
		{name: "coded boundary splits channel frame", candidate: valid, reference: valid, profile: SignalProfile{SampleRate: 16000, Channels: 2, TotalSamples: 4, CodedSamples: 1}, intent: IntentNearExact},
		{name: "PCM length splits channel frame", candidate: valid[:3], reference: valid[:3], profile: SignalProfile{SampleRate: 16000, Channels: 2, TotalSamples: 3, CodedSamples: 2}, intent: IntentNearExact},
		{name: "invalid sample rate", candidate: valid, reference: valid, profile: SignalProfile{Channels: 2, TotalSamples: 4, CodedSamples: 2}, intent: IntentNearExact},
		{name: "invalid channel count", candidate: valid, reference: valid, profile: SignalProfile{SampleRate: 16000, TotalSamples: 4, CodedSamples: 2}, intent: IntentNearExact},
		{name: "unsupported intent", candidate: valid, reference: valid, profile: base, intent: ParityIntent(99)},
		{name: "candidate NaN", candidate: []float32{0, float32(math.NaN()), 0, 0}, reference: valid, profile: base, intent: IntentNearExact},
		{name: "reference positive infinity", candidate: valid, reference: []float32{0, 0, float32(math.Inf(1)), 0}, profile: base, intent: IntentNearExact},
		{name: "reference negative infinity", candidate: valid, reference: []float32{0, 0, 0, float32(math.Inf(-1))}, profile: base, intent: IntentNearExact},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateParityInputs(tc.candidate, tc.reference, tc.profile, tc.intent); err == nil {
				t.Fatal("validateParityInputs accepted invalid parity input")
			}
		})
	}
}

func TestValidateParityInputsDocumentsTotalSamplesDefault(t *testing.T) {
	x := []float32{0.1, -0.1, 0.2, -0.2}
	p := SignalProfile{SampleRate: 16000, Channels: 2, CodedSamples: len(x)}
	got, err := validateParityInputs(x, append([]float32(nil), x...), p, IntentNearExact)
	if err != nil {
		t.Fatalf("zero TotalSamples default: %v", err)
	}
	if got.TotalSamples != len(x) {
		t.Fatalf("normalized TotalSamples=%d, want %d", got.TotalSamples, len(x))
	}
}

func TestCodedTierUsesWaveformForUnsupportedChannelCount(t *testing.T) {
	p := CodedProfile(48000, 3, 1440)
	if tier := p.codedTier(); tier != TierWaveform {
		t.Fatalf("codedTier for %d channels=%s, want %s", p.Channels, tier, TierWaveform)
	}
}

func TestScoreParityRegionDoesNotDowngradeOpusCompareError(t *testing.T) {
	wantErr := errors.New("opus_compare unavailable")
	x := sine(480, 1, 440)
	_, err := scoreParityRegion(x, x, CodedProfile(48000, 1, len(x)), TierOpusCompare,
		func([]float32, []float32, int, int, int) (QualityComparison, error) {
			return QualityComparison{}, wantErr
		})
	if !errors.Is(err, wantErr) {
		t.Fatalf("scoreParityRegion error=%v, want %v", err, wantErr)
	}
}

func TestValidateComparablePCMRejectsInvalidInputs(t *testing.T) {
	valid := []float32{0, 0.25, -0.25, 0.5}
	cases := []struct {
		name      string
		candidate []float32
		reference []float32
		rate      int
		channels  int
		maxDelay  int
	}{
		{name: "empty", candidate: nil, reference: nil, rate: 48000, channels: 1},
		{name: "unequal lengths", candidate: valid, reference: valid[:2], rate: 48000, channels: 1},
		{name: "unsupported channels", candidate: valid, reference: valid, rate: 48000, channels: 3},
		{name: "unaligned stereo", candidate: valid[:3], reference: valid[:3], rate: 48000, channels: 2},
		{name: "negative delay", candidate: valid, reference: valid, rate: 48000, channels: 1, maxDelay: -1},
		{name: "candidate NaN", candidate: []float32{0, float32(math.NaN())}, reference: []float32{0, 0}, rate: 48000, channels: 1},
		{name: "reference infinity", candidate: []float32{0, 0}, reference: []float32{0, float32(math.Inf(1))}, rate: 48000, channels: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateComparablePCM(tc.candidate, tc.reference, tc.rate, tc.channels, tc.maxDelay); err == nil {
				t.Fatal("validateComparablePCM accepted invalid comparison input")
			}
		})
	}
}

func TestQualityBarCheckRejectsNonFiniteMetricsAndThresholds(t *testing.T) {
	validBar := QualityBar{MinQ: 0, MinCorr: 0.9, RMSLo: 0.9, RMSHi: 1.1}
	validCmp := QualityComparison{Q: 20, Corr: 1, RMSRatio: 1}
	cases := []struct {
		name string
		bar  QualityBar
		cmp  QualityComparison
	}{
		{name: "Q NaN", bar: validBar, cmp: QualityComparison{Q: math.NaN(), Corr: 1, RMSRatio: 1}},
		{name: "Q positive infinity", bar: validBar, cmp: QualityComparison{Q: math.Inf(1), Corr: 1, RMSRatio: 1}},
		{name: "Q negative infinity while enabled", bar: validBar, cmp: QualityComparison{Q: math.Inf(-1), Corr: 1, RMSRatio: 1}},
		{name: "correlation NaN", bar: validBar, cmp: QualityComparison{Q: 20, Corr: math.NaN(), RMSRatio: 1}},
		{name: "correlation infinity", bar: validBar, cmp: QualityComparison{Q: 20, Corr: math.Inf(1), RMSRatio: 1}},
		{name: "RMS NaN", bar: validBar, cmp: QualityComparison{Q: 20, Corr: 1, RMSRatio: math.NaN()}},
		{name: "RMS infinity", bar: validBar, cmp: QualityComparison{Q: 20, Corr: 1, RMSRatio: math.Inf(1)}},
		{name: "NaN Q threshold", bar: QualityBar{MinQ: math.NaN()}, cmp: validCmp},
		{name: "infinite correlation threshold", bar: QualityBar{MinCorr: math.Inf(1)}, cmp: validCmp},
		{name: "inverted RMS bounds", bar: QualityBar{RMSLo: 1.1, RMSHi: 0.9}, cmp: validCmp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if fails := tc.bar.Check(tc.cmp); len(fails) == 0 {
				t.Fatal("QualityBar.Check accepted non-finite metric or invalid threshold")
			}
		})
	}
}

func TestQualityBarCheckPreservesDisabledMetricSemantics(t *testing.T) {
	t.Run("waveform-only Q sentinel", func(t *testing.T) {
		bar := QualityBar{MinQ: math.Inf(-1), MinCorr: 0.9, RMSLo: 0.9, RMSHi: 1.1}
		cmp := QualityComparison{Q: math.Inf(-1), Corr: 1, RMSRatio: 1}
		if fails := bar.Check(cmp); len(fails) != 0 {
			t.Fatalf("disabled Q sentinel rejected: %v", fails)
		}
	})
	t.Run("Q-only bar with finite unchecked waveform metrics", func(t *testing.T) {
		bar := QualityBar{MinQ: 0}
		cmp := QualityComparison{Q: 20}
		if fails := bar.Check(cmp); len(fails) != 0 {
			t.Fatalf("finite unchecked zero metrics rejected: %v", fails)
		}
	})
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
