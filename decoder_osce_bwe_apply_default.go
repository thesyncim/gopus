//go:build !gopus_osce

package gopus

import (
	"github.com/thesyncim/gopus/internal/silk"
)

func (d *Decoder) maybeApplyOSCEBWEPostSilk(
	_ []float32,
	_ int,
	_ Mode,
	_ silk.Bandwidth,
	_ bool,
) bool {
	return false
}

// osceBWEMarkInactiveIfModeIneligible is a no-op stub on the default build.
func (d *Decoder) osceBWEMarkInactiveIfModeIneligible(_ Mode, _ Bandwidth, _ []float32, _ int, _ bool) {
}

// resetOSCEBWEPostfilterState is a no-op on the default build.
func (d *Decoder) resetOSCEBWEPostfilterState() {}

type decoderOSCEBWEState struct{}

func (d *Decoder) installOSCELACESilkPostfilterHook(_ Mode, _ silk.Bandwidth, _ bool) {}

func (d *Decoder) clearOSCELACESilkPostfilterHook() {}

// osceLACEMarkInactiveIfModeIneligible is a no-op stub on the default
// build. The `gopus_osce` build provides the
// real implementation in `decoder_osce_lace_apply.go`.
func (d *Decoder) osceLACEMarkInactiveIfModeIneligible(_ Mode, _ Bandwidth) {}

// resetOSCELACEPostfilterState is a no-op on the default build.
func (d *Decoder) resetOSCELACEPostfilterState(_ bool) {}

type decoderOSCELACEState struct{}

func (d *Decoder) initOSCELossHook() {}
