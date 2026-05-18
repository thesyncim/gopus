//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
)

// decoderOSCEBWEState carries decoder-side OSCE BWE runtime bookkeeping under
// the explicit quarantine build. The `osceBWEModel` field follows the same
// pattern as the FARGAN / Predictor bindings: it is non-nil once
// `SetDNNBlob` has successfully bound an OSCE BWE-capable weights blob.
type decoderOSCEBWEState struct {
	osceBWEModel    *osceBWE.Model
	osceBWERuntime  osceBWE.State
	osceBWEFeatures osceBWE.FeatureState

	// Pre-allocated working buffers for the post-SILK BWE forward pass so
	// the decoder hot path does not allocate per-frame.
	applyIn16     [320]float32 // 20 ms @ 16 kHz max
	applyIn16Int  [320]int16   // signed-int16 view consumed by the feature extractor
	applyOut48    [3 * 320]float32
	applyFeatures [2 * osceBWE.FeatureDim]float32
}
