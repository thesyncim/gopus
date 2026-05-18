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
//
// libopus keeps a per-channel `silk_OSCE_BWE_struct` in each
// `silk_channel_state` and runs `osce_bwe(...)` independently on each SILK
// lowband channel (see `silk/dec_API.c` around the `OSCE_MODE_SILK_BBWE`
// branch). The gopus runtime mirrors that with one `osceBWE.State` per
// channel; index 0 carries the mid/left channel state, index 1 the side/
// right channel state. Mono decode paths only use slot 0.
type decoderOSCEBWEState struct {
	osceBWEModel    *osceBWE.Model
	osceBWERuntime  [2]osceBWE.State
	osceBWEFeatures [2]osceBWE.FeatureState

	// Pre-allocated working buffers for the post-SILK BWE forward pass so
	// the decoder hot path does not allocate per-frame. The input/output
	// buffers are sized for one channel; stereo runs the forward pass
	// sequentially on each channel re-using the same scratch.
	applyIn16     [320]float32 // 20 ms @ 16 kHz max
	applyIn16Int  [320]int16   // signed-int16 view consumed by the feature extractor
	applyOut48    [3 * 320]float32
	applyFeatures [2 * osceBWE.FeatureDim]float32
}
