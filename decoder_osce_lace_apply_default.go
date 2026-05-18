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

// osceLACEMarkInactiveIfModeIneligible is a no-op stub on the default
// build. The quarantined `gopus_unsupported_controls` build provides the
// real implementation in `decoder_osce_lace_apply.go`.
func (d *Decoder) osceLACEMarkInactiveIfModeIneligible(_ Mode, _ Bandwidth) {}
