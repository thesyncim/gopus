package qualitycompare

import (
	"fmt"
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
// Quality metrics complement exact comparisons; they do not excuse differences
// against a matching libopus build. Tests that require exactness select the same
// scalar or SIMD instruction lane for Go and libopus and compare the relevant
// packets, ranges, or PCM samples directly. See the parity reports for the tested
// configurations and coverage.

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
	TotalSamples int // Interleaved sample count; zero infers the nonempty PCM length.
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
	if p.SampleRate == opusCompareRate && (p.Channels == 1 || p.Channels == 2) && p.CodedSamples/p.Channels >= opusCompareMinPerChan {
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

// Bars for the waveform tier use the correlation and RMS thresholds recorded
// for the corresponding libopus comparisons. The reports describe the tested
// cases; these thresholds measure waveform similarity rather than exactness.
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

func validateParityInputs(candidate, reference []float32, p SignalProfile, intent ParityIntent) (SignalProfile, error) {
	if len(candidate) == 0 || len(reference) == 0 {
		return p, fmt.Errorf("parity comparison requires nonempty candidate and reference PCM")
	}
	if len(candidate) != len(reference) {
		return p, fmt.Errorf("PCM sample count mismatch: candidate=%d reference=%d", len(candidate), len(reference))
	}
	if p.SampleRate <= 0 {
		return p, fmt.Errorf("profile sample rate must be positive (got %d)", p.SampleRate)
	}
	if p.Channels <= 0 {
		return p, fmt.Errorf("profile channel count must be positive (got %d)", p.Channels)
	}
	if len(candidate)%p.Channels != 0 {
		return p, fmt.Errorf("PCM sample count %d is not aligned to %d channels", len(candidate), p.Channels)
	}
	if p.TotalSamples == 0 {
		p.TotalSamples = len(candidate)
	} else if p.TotalSamples != len(candidate) {
		return p, fmt.Errorf("profile total samples=%d does not match PCM length=%d", p.TotalSamples, len(candidate))
	}
	if p.CodedSamples < 0 || p.CodedSamples > p.TotalSamples {
		return p, fmt.Errorf("coded sample count %d is outside [0,%d]", p.CodedSamples, p.TotalSamples)
	}
	if p.CodedSamples%p.Channels != 0 {
		return p, fmt.Errorf("coded sample count %d is not aligned to %d channels", p.CodedSamples, p.Channels)
	}
	if intent != IntentNearExact && intent != IntentRFCConformance {
		return p, fmt.Errorf("unsupported parity intent %d", intent)
	}
	for i := range candidate {
		if math.IsNaN(float64(candidate[i])) || math.IsInf(float64(candidate[i]), 0) {
			return p, fmt.Errorf("candidate PCM[%d] is non-finite: %v", i, candidate[i])
		}
		if math.IsNaN(float64(reference[i])) || math.IsInf(float64(reference[i]), 0) {
			return p, fmt.Errorf("reference PCM[%d] is non-finite: %v", i, reference[i])
		}
	}
	return p, nil
}

type parityComparer func(candidate, reference []float32, sampleRate, channels, maxDelay int) (QualityComparison, error)

func scoreParityRegion(candidate, reference []float32, p SignalProfile, tier MetricTier, compare parityComparer) (QualityComparison, error) {
	switch tier {
	case TierOpusCompare:
		return compare(candidate, reference, p.SampleRate, p.Channels, delaySearchWindow(p.Channels))
	case TierWaveform:
		corr, rms := waveformCorrelationRMS(candidate, reference)
		return QualityComparison{Q: 0, Corr: corr, RMSRatio: rms}, nil
	default:
		return QualityComparison{}, fmt.Errorf("unsupported metric tier %d", tier)
	}
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
	var err error
	p, err = validateParityInputs(candidate, reference, p, intent)
	if err != nil {
		t.Fatalf("%s invalid parity input: %v", label, err)
		return ParityVerdict{}
	}

	verdict := ParityVerdict{Profile: p}
	assertRegion := func(name string, lo, hi int, tier MetricTier) {
		if hi <= lo {
			return
		}
		cand, ref := candidate[lo:hi], reference[lo:hi]
		cmp, err := scoreParityRegion(cand, ref, p, tier, CompareDecodedFloat32)
		if err != nil {
			t.Fatalf("%s [%s] required %s metric failed: %v", label, name, tier, err)
			return
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
	if len(verdict.Regions) == 0 {
		t.Fatalf("%s has no nonempty PCM region to compare", label)
	}
	return verdict
}
