package qualitycompare

import (
	"math"
	"testing"
)

// Self-selecting parity gate for decoded PCM.
//
// The trap this avoids: opus_compare's psychoacoustic Q (RFC 8251) is meaningful
// only for coded audio at 48 kHz with at least 10 ms of content. On resampled
// (sub-48 kHz), too-short, or extrapolated (PLC/concealment) output it is not a
// valid quality metric and returns nonsense (often Q < 0) even when the waveform
// matches libopus to a few 1e-3. Letting each test decide when Q applies is how
// it gets misapplied. AssertParity removes that decision from the caller: it
// derives the only valid metric from an objective SignalProfile and never lets Q
// score concealed or sub-rate samples.
//
// Why this is build-invariant where bit-exact comparison is not: Q, correlation,
// and RMS ratio are statistical/perceptual measures, so a 1-ULP FMA-contraction
// difference between build configs (arm64 vs amd64 vs purego) moves them far
// below their bars. Bit-exact oracles, which do break on such differences, are a
// separate tier reserved for isolated algorithmic kernels and are enforced across
// the whole build-config matrix (see Makefile test-build-config-matrix).

// opus_compare validity thresholds (RFC 8251 / libopus opus_compare.c): the tool
// is defined for 48 kHz input and needs enough content for its per-band model.
const (
	opusCompareRate       = 48000
	opusCompareMinPerChan = 480 // 10 ms @ 48 kHz
)

// SignalProfile objectively describes a decoded stream so the metric can be
// selected without any per-test judgement. CodedSamples is the interleaved count
// of leading samples produced by coded frames; the remainder (TotalSamples -
// CodedSamples) is concealment/extrapolation, where opus_compare Q is invalid.
// For a fully coded stream, set CodedSamples == TotalSamples (or use
// CodedProfile).
type SignalProfile struct {
	SampleRate   int
	Channels     int
	TotalSamples int
	CodedSamples int
}

// CodedProfile builds a profile for a stream with no concealment.
func CodedProfile(sampleRate, channels, totalSamples int) SignalProfile {
	return SignalProfile{SampleRate: sampleRate, Channels: channels, TotalSamples: totalSamples, CodedSamples: totalSamples}
}

// MetricTier is the trusted metric selected for a region.
type MetricTier int

const (
	// TierWaveform: opus_compare Q is invalid (sub-48 kHz, < 10 ms, or
	// concealment); correlation + RMS ratio are the trusted metric.
	TierWaveform MetricTier = iota
	// TierOpusCompare: RFC 8251 Q is valid; correlation + RMS reported alongside.
	TierOpusCompare
)

func (m MetricTier) String() string {
	if m == TierOpusCompare {
		return "opus_compare-Q"
	}
	return "waveform-corr/RMS"
}

// codedTier returns the only valid metric for the coded portion of p.
func (p SignalProfile) codedTier() MetricTier {
	if p.SampleRate == opusCompareRate && p.Channels > 0 && p.CodedSamples/p.Channels >= opusCompareMinPerChan {
		return TierOpusCompare
	}
	return TierWaveform
}

// ParityIntent selects bar strictness, anchored to external references only.
type ParityIntent int

const (
	// IntentNearExact requires gopus to track libopus as closely as libopus
	// tracks itself across builds (the measured SILK/CELT/Hybrid envelope). This
	// is the default for decode/encode parity.
	IntentNearExact ParityIntent = iota
	// IntentRFCConformance requires only RFC 8251 conformance (Q >= 0).
	IntentRFCConformance
)

// Bars for the waveform tier, anchored to the same external references as the
// opus_compare bars: the near-exact envelope is libopus's own cross-build
// waveform agreement (corr >= 0.997, RMS within +/-2%), well inside which gopus
// sits on every covered case; the RFC envelope is the looser conformance floor.
var (
	waveformBarNearExact = QualityBar{MinQ: math.Inf(-1), MinCorr: 0.997, RMSLo: 0.98, RMSHi: 1.02, Desc: "near-exact waveform vs libopus (opus_compare N/A here)"}
	waveformBarRFC       = QualityBar{MinQ: math.Inf(-1), MinCorr: 0.985, RMSLo: 0.97, RMSHi: 1.03, Desc: "RFC-floor waveform vs libopus (opus_compare N/A here)"}
)

func barFor(tier MetricTier, intent ParityIntent) QualityBar {
	switch {
	case tier == TierOpusCompare && intent == IntentNearExact:
		return QualityBarNearExact
	case tier == TierOpusCompare:
		return QualityBarRFC
	case intent == IntentNearExact:
		return waveformBarNearExact
	default:
		return waveformBarRFC
	}
}

// RegionVerdict is the result for one region (coded or concealed) of a stream.
type RegionVerdict struct {
	Name string
	Tier MetricTier
	Cmp  QualityComparison
	Bar  QualityBar
}

// ParityVerdict is the full result of AssertParity.
type ParityVerdict struct {
	Profile SignalProfile
	Regions []RegionVerdict
}

// delaySearchWindow bounds the opus_compare delay search. Decode-vs-libopus
// streams that decode the same packets are sample-aligned, but codec startup and
// resampler group delay add a few samples of jitter; a 5 ms window absorbs that
// without letting the search hide a real misalignment.
func delaySearchWindow(channels int) int {
	return 240 * channels // 5 ms @ 48 kHz
}

// AssertParity is the single self-selecting parity gate for decoded PCM. It
// splits the stream into its coded prefix and concealed tail (per
// profile.CodedSamples), scores each region with the only metric valid for it
// (opus_compare Q for >= 10 ms of coded 48 kHz audio, waveform corr/RMS
// otherwise), and gates each on the externally anchored bar for that tier and
// intent. The caller supplies an objective profile and an intent, never a
// threshold. It fails t on any region miss and returns the full verdict.
func AssertParity(t *testing.T, candidate, reference []float32, p SignalProfile, intent ParityIntent, label string) ParityVerdict {
	t.Helper()
	n := min(len(candidate), len(reference))
	if p.TotalSamples == 0 || p.TotalSamples > n {
		p.TotalSamples = n
	}
	if p.CodedSamples > p.TotalSamples {
		p.CodedSamples = p.TotalSamples
	}
	if p.CodedSamples < 0 {
		p.CodedSamples = 0
	}

	verdict := ParityVerdict{Profile: p}
	assertRegion := func(name string, lo, hi int, tier MetricTier) {
		if hi <= lo {
			return
		}
		cand, ref := candidate[lo:hi], reference[lo:hi]
		var cmp QualityComparison
		if tier == TierOpusCompare {
			c, err := CompareDecodedFloat32(cand, ref, p.SampleRate, p.Channels, delaySearchWindow(p.Channels))
			if err != nil {
				// opus_compare unavailable for this segment after all; fall back to
				// the waveform tier rather than skipping the region.
				tier = TierWaveform
				corr, rms := waveformCorrelationRMS(cand, ref)
				cmp = QualityComparison{Q: math.Inf(-1), Corr: corr, RMSRatio: rms}
			} else {
				cmp = c
			}
		} else {
			corr, rms := waveformCorrelationRMS(cand, ref)
			cmp = QualityComparison{Q: 0, Corr: corr, RMSRatio: rms}
		}
		bar := barFor(tier, intent)
		rv := RegionVerdict{Name: name, Tier: tier, Cmp: cmp, Bar: bar}
		verdict.Regions = append(verdict.Regions, rv)
		t.Logf("%s [%s/%s]: Q=%.2f corr=%.6f rms=%.4f delay=%d (bar: %s)",
			label, name, tier, cmp.Q, cmp.Corr, cmp.RMSRatio, cmp.BestDelay, bar.Desc)
		if fails := bar.Check(cmp); len(fails) > 0 {
			t.Fatalf("%s [%s] below libopus parity bar [%s]: %v", label, name, bar.Desc, fails)
		}
	}

	assertRegion("coded", 0, p.CodedSamples, p.codedTier())
	assertRegion("concealed", p.CodedSamples, p.TotalSamples, TierWaveform)
	return verdict
}
