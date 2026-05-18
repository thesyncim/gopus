//go:build !gopus_unsupported_controls
// +build !gopus_unsupported_controls

package gopus

import "github.com/thesyncim/gopus/silk"

// maybeApplyOSCELACEPostSilk is a no-op outside of the explicit
// `gopus_unsupported_controls` build. Default builds keep the OSCE
// LACE/NoLACE postfilter quarantined so the standard silk_resampler
// output is always used.
func (d *Decoder) maybeApplyOSCELACEPostSilk(
	_ []float32,
	_ int,
	_ Mode,
	_ silk.Bandwidth,
	_ bool,
) bool {
	return false
}
