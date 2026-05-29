package testvectors

import "github.com/thesyncim/gopus/internal/qualitycompare"

// This file re-exports the canonical quality comparator (moved to
// internal/qualitycompare so the root gopus package tests can use it too) under
// the names the testvectors callers already use. The comparator logic,
// delay-search, thresholds, and bars all live in internal/qualitycompare; these
// are thin aliases only.

// QualityComparison is the result of a trusted opus_compare-based comparison.
type QualityComparison = qualitycompare.QualityComparison

// QualityBar is a trusted parity threshold.
type QualityBar = qualitycompare.QualityBar

// Trusted quality bars.
var (
	QualityBarNearExact = qualitycompare.QualityBarNearExact
	QualityBarRFC       = qualitycompare.QualityBarRFC
)

// Comparator and opus_compare entry points.
var (
	CompareDecodedFloat32                     = qualitycompare.CompareDecodedFloat32
	ComputeOpusCompareQuality                 = qualitycompare.ComputeOpusCompareQuality
	ComputeOpusCompareQualityFloat32          = qualitycompare.ComputeOpusCompareQualityFloat32
	ComputeOpusCompareQualityFloat32WithDelay = qualitycompare.ComputeOpusCompareQualityFloat32WithDelay
	QualityBarForMode                         = qualitycompare.QualityBarForMode
	AssertQuality                             = qualitycompare.AssertQuality
)
