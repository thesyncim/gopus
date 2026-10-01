//go:build gopus_dred || gopus_osce

package multistream

import "github.com/thesyncim/gopus/internal/libopustest"

func buildMultistreamReferenceHelper(cfg libopustest.CHelperConfig) (string, error) {
	return libopustest.BuildDNNCHelper("", cfg)
}

// buildMultistreamFloatShadowReferenceHelper pairs probes of streamState's
// float decoder with the floating-point DNN build selected by the Go tags.
// Fixed-point public decoders use a separate opus_res path, but streamState
// retains this float shadow to advance shared decoder state.
func buildMultistreamFloatShadowReferenceHelper(cfg libopustest.CHelperConfig) (string, error) {
	return libopustest.BuildDNNCHelper("", cfg)
}
