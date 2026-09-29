package qualitycompare

import (
	"fmt"
	"math"
	"testing"
)

// This file is the single, canonical quality comparator for gopus-vs-libopus
// parity tests. It standardizes on opus_compare — the reference quality tool
// shipped with libopus and the metric RFC 8251 defines conformance with — so the
// trust in these comparisons does not depend on gopus: it is the same tool and
// metric the whole Opus ecosystem (and the spec) uses.
//
// Quality comparison policy:
//   - opus_compare Q (0..100, higher == closer) is the primary, trusted metric,
//     delay-searched against the reference (libopus-decoded PCM or packets).
//   - Waveform correlation and RMS ratio are reported as secondary diagnostics.
//   - Exact packet, range, and sample checks remain separate gates wherever the
//     paired reference supports them. This comparator measures waveform quality
//     and cannot establish exact equality.
//   - QualityBar thresholds are calibrated against RFC 8251 criteria and
//     recorded libopus comparisons. See the parity reports for their tested
//     scope; a threshold does not permit mismatch against a matching C build.

// QualityComparison is the result of a trusted opus_compare-based comparison.
type QualityComparison struct {
	Q         float64 // opus_compare Opus quality metric (RFC 8251), higher == closer.
	BestDelay int     // sample delay (per channel * channels) that maximized Q.
	Corr      float64 // secondary: waveform Pearson correlation in [-1, 1].
	RMSRatio  float64 // secondary: RMS(candidate) / RMS(reference).
}

// CompareDecodedFloat32 is the canonical comparator: it scores candidate decoded
// PCM against a reference (typically libopus-decoded) using delay-searched
// opus_compare, plus correlation/RMS diagnostics. 48 kHz interleaved PCM.
func CompareDecodedFloat32(candidate, reference []float32, sampleRate, channels, maxDelay int) (QualityComparison, error) {
	if err := validateComparablePCM(candidate, reference, sampleRate, channels, maxDelay); err != nil {
		return QualityComparison{}, err
	}
	q, delay, err := ComputeOpusCompareQualityFloat32WithDelay(candidate, reference, sampleRate, channels, maxDelay)
	if err != nil {
		return QualityComparison{}, err
	}
	corr, rms := waveformCorrelationRMS(candidate, reference)
	return QualityComparison{Q: q, BestDelay: delay, Corr: corr, RMSRatio: rms}, nil
}

func validateComparablePCM(candidate, reference []float32, sampleRate, channels, maxDelay int) error {
	if len(candidate) == 0 || len(reference) == 0 {
		return fmt.Errorf("PCM comparison requires nonempty candidate and reference")
	}
	if len(candidate) != len(reference) {
		return fmt.Errorf("PCM sample count mismatch: candidate=%d reference=%d", len(candidate), len(reference))
	}
	if sampleRate != 48000 {
		return fmt.Errorf("opus_compare requires 48 kHz PCM (got %d Hz)", sampleRate)
	}
	if channels != 1 && channels != 2 {
		return fmt.Errorf("opus_compare supports mono or stereo PCM (got %d channels)", channels)
	}
	if len(candidate)%channels != 0 {
		return fmt.Errorf("PCM sample count %d is not aligned to %d channels", len(candidate), channels)
	}
	if maxDelay < 0 {
		return fmt.Errorf("maximum delay must be nonnegative (got %d)", maxDelay)
	}
	for i := range candidate {
		if math.IsNaN(float64(candidate[i])) || math.IsInf(float64(candidate[i]), 0) {
			return fmt.Errorf("candidate PCM[%d] is non-finite: %v", i, candidate[i])
		}
		if math.IsNaN(float64(reference[i])) || math.IsInf(float64(reference[i]), 0) {
			return fmt.Errorf("reference PCM[%d] is non-finite: %v", i, reference[i])
		}
	}
	return nil
}

// waveformCorrelationRMS computes Pearson correlation and RMS ratio over the
// common prefix. These are secondary quality diagnostics, not exactness checks.
func waveformCorrelationRMS(a, b []float32) (corr, rmsRatio float64) {
	n := min(len(b), len(a))
	if n == 0 {
		return 0, 0
	}
	var sumA, sumB, sumASq, sumBSq, cov float64
	for i := range n {
		fa, fb := float64(a[i]), float64(b[i])
		sumA += fa
		sumB += fb
		sumASq += fa * fa
		sumBSq += fb * fb
	}
	meanA, meanB := sumA/float64(n), sumB/float64(n)
	var varA, varB float64
	for i := range n {
		da, db := float64(a[i])-meanA, float64(b[i])-meanB
		cov += da * db
		varA += da * da
		varB += db * db
	}
	if varA > 0 && varB > 0 {
		corr = cov / math.Sqrt(varA*varB)
	} else if varA == 0 && varB == 0 {
		corr = 1
	}
	rmsA := math.Sqrt(sumASq / float64(n))
	rmsB := math.Sqrt(sumBSq / float64(n))
	if rmsB > 0 {
		rmsRatio = rmsA / rmsB
	} else if rmsA == 0 {
		rmsRatio = 1
	}
	return corr, rmsRatio
}

// QualityBar holds waveform-quality thresholds for comparisons with libopus.
// A zero value means "unchecked".
type QualityBar struct {
	MinQ    float64 // absolute opus_compare floor vs the libopus reference.
	MinCorr float64 // waveform correlation floor.
	RMSLo   float64 // RMS ratio lower bound (0 == unchecked).
	RMSHi   float64 // RMS ratio upper bound (0 == unchecked).
	Desc    string  // human-readable basis, e.g. "near-exact (matches SILK/CELT)".
}

// Quality bars measure decoded-waveform agreement with libopus. They complement
// exact packet and sample comparisons; a quality pass does not establish bit
// equality or classify an unexplained difference as acceptable. A validated
// rounding allowance also requires the evidence in reports/parity-target.md.
var (
	QualityBarNearExact = QualityBar{MinQ: 20.0, MinCorr: 0.997, RMSLo: 0.98, RMSHi: 1.02, Desc: "near-exact vs libopus (SILK/CELT/Hybrid bar)"}
	QualityBarRFC       = QualityBar{MinQ: 0.0, MinCorr: 0.985, RMSLo: 0.97, RMSHi: 1.03, Desc: "RFC 8251 conformance floor"}
)

// QualityBarForMode returns the trusted bar for a decode-parity case by dominant
// mode. SILK, CELT and Hybrid use the same decoded-waveform quality bar.
func QualityBarForMode(mode string, channels int) QualityBar {
	switch mode {
	case "silk", "celt", "hybrid":
		return QualityBarNearExact
	default:
		return QualityBarRFC
	}
}

// Check reports the ways cmp fails bar (empty slice == pass).
func (bar QualityBar) Check(cmp QualityComparison) []string {
	var fails []string
	if math.IsNaN(bar.MinQ) || (math.IsInf(bar.MinQ, 0) && !math.IsInf(bar.MinQ, -1)) {
		fails = append(fails, fmt.Sprintf("invalid minimum Q threshold: %v", bar.MinQ))
	}
	if math.IsNaN(bar.MinCorr) || math.IsInf(bar.MinCorr, 0) || bar.MinCorr < 0 || bar.MinCorr > 1 {
		fails = append(fails, fmt.Sprintf("invalid minimum correlation threshold: %v", bar.MinCorr))
	}
	if math.IsNaN(bar.RMSLo) || math.IsInf(bar.RMSLo, 0) || bar.RMSLo < 0 {
		fails = append(fails, fmt.Sprintf("invalid RMS lower threshold: %v", bar.RMSLo))
	}
	if math.IsNaN(bar.RMSHi) || math.IsInf(bar.RMSHi, 0) || bar.RMSHi < 0 {
		fails = append(fails, fmt.Sprintf("invalid RMS upper threshold: %v", bar.RMSHi))
	}
	if bar.RMSLo > 0 && bar.RMSHi > 0 && bar.RMSLo > bar.RMSHi {
		fails = append(fails, fmt.Sprintf("invalid RMS bounds: %.4f > %.4f", bar.RMSLo, bar.RMSHi))
	}
	if math.IsNaN(cmp.Q) || (math.IsInf(cmp.Q, 0) && !(math.IsInf(cmp.Q, -1) && math.IsInf(bar.MinQ, -1))) {
		fails = append(fails, fmt.Sprintf("Q is non-finite: %v", cmp.Q))
	}
	if math.IsNaN(cmp.Corr) || math.IsInf(cmp.Corr, 0) {
		fails = append(fails, fmt.Sprintf("correlation is non-finite: %v", cmp.Corr))
	}
	if math.IsNaN(cmp.RMSRatio) || math.IsInf(cmp.RMSRatio, 0) {
		fails = append(fails, fmt.Sprintf("RMS ratio is non-finite: %v", cmp.RMSRatio))
	}
	if cmp.Q < bar.MinQ {
		fails = append(fails, fmt.Sprintf("Q=%.2f < %.2f", cmp.Q, bar.MinQ))
	}
	if bar.MinCorr > 0 && cmp.Corr < bar.MinCorr {
		fails = append(fails, fmt.Sprintf("corr=%.6f < %.6f", cmp.Corr, bar.MinCorr))
	}
	if bar.RMSLo > 0 && cmp.RMSRatio < bar.RMSLo {
		fails = append(fails, fmt.Sprintf("rms=%.4f < %.4f", cmp.RMSRatio, bar.RMSLo))
	}
	if bar.RMSHi > 0 && cmp.RMSRatio > bar.RMSHi {
		fails = append(fails, fmt.Sprintf("rms=%.4f > %.4f", cmp.RMSRatio, bar.RMSHi))
	}
	return fails
}

// AssertQuality fails t if cmp does not clear bar, logging the trusted basis and
// the measured metrics. This is the single assertion all migrated quality-parity
// tests should use, so the bar (and its libopus-anchored rationale) lives in one
// place rather than scattered per-test constants.
func AssertQuality(t *testing.T, cmp QualityComparison, bar QualityBar, label string) {
	t.Helper()
	t.Logf("%s: Q=%.2f delay=%d corr=%.6f rms=%.4f (bar: %s, minQ=%.1f)",
		label, cmp.Q, cmp.BestDelay, cmp.Corr, cmp.RMSRatio, bar.Desc, bar.MinQ)
	if fails := bar.Check(cmp); len(fails) > 0 {
		t.Fatalf("%s quality below libopus parity bar [%s]: %v", label, bar.Desc, fails)
	}
}
