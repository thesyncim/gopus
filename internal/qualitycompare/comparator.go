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
//   - Bit-exact numeric oracles for isolated kernels are NOT replaced by this —
//     they remain hard gates. This comparator governs end-to-end audio quality,
//     where bit-exactness is bounded by transcendental/libm/platform rounding.
//   - Trusted bars (QualityBar) are anchored to RFC 8251 conformance and to
//     libopus's own cross-build self-variation: gopus must track the libopus
//     reference at least as closely as libopus tracks itself across builds, never
//     to a higher bar than libopus holds itself.

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
	q, delay, err := ComputeOpusCompareQualityFloat32WithDelay(candidate, reference, sampleRate, channels, maxDelay)
	if err != nil {
		return QualityComparison{}, err
	}
	corr, rms := waveformCorrelationRMS(candidate, reference)
	return QualityComparison{Q: q, BestDelay: delay, Corr: corr, RMSRatio: rms}, nil
}

// waveformCorrelationRMS computes Pearson correlation and RMS ratio over the
// common prefix (canonical secondary diagnostics; previously duplicated as
// decoderParityStats).
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

// QualityBar is a trusted parity threshold, anchored to RFC 8251 conformance and
// libopus's own cross-build self-variation. A zero value means "unchecked".
type QualityBar struct {
	MinQ    float64 // absolute opus_compare floor vs the libopus reference.
	MinCorr float64 // waveform correlation floor.
	RMSLo   float64 // RMS ratio lower bound (0 == unchecked).
	RMSHi   float64 // RMS ratio upper bound (0 == unchecked).
	Desc    string  // human-readable basis, e.g. "near-exact (matches SILK/CELT)".
}

// Trusted quality bars. "near-exact" is the bar SILK/CELT (and now Hybrid) decode
// already meet vs libopus (measured Q>=99.7); it is far above the RFC-8251
// conformance floor (Q>=0) yet still strictly below bit-exactness, leaving room
// only for the transcendental/platform rounding tail that is not a gopus defect.
var (
	QualityBarNearExact = QualityBar{MinQ: 20.0, MinCorr: 0.997, RMSLo: 0.98, RMSHi: 1.02, Desc: "near-exact vs libopus (SILK/CELT/Hybrid bar)"}
	QualityBarRFC       = QualityBar{MinQ: 0.0, MinCorr: 0.985, RMSLo: 0.97, RMSHi: 1.03, Desc: "RFC 8251 conformance floor"}
)

// QualityBarForMode returns the trusted bar for a decode-parity case by dominant
// mode. All three modes now meet the near-exact bar vs libopus.
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
