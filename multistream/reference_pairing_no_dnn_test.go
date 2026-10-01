//go:build !gopus_dred && !gopus_osce

package multistream

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func buildMultistreamReferenceHelper(cfg libopustest.CHelperConfig) (string, error) {
	return libopustest.BuildCHelper(pairMultistreamReference(cfg))
}

// buildMultistreamFloatShadowReferenceHelper pairs probes of streamState's
// float decoder with the matching floating-point libopus build. Fixed-point
// public decoders use a separate opus_res path, but streamState retains this
// float shadow to advance shared decoder state.
func buildMultistreamFloatShadowReferenceHelper(cfg libopustest.CHelperConfig) (string, error) {
	archive := libopustest.RefPath(".libs", "libopus.a")
	if extsupport.QEXT {
		cfg.QEXTRef = true
		archive = libopustest.QEXTRefPath(".libs", "libopus.a")
	}
	cfg.Libs = append([]string{archive}, cfg.Libs...)
	return libopustest.BuildCHelper(cfg)
}
