//go:build !gopus_unsupported_controls
// +build !gopus_unsupported_controls

package multistream

import "github.com/thesyncim/gopus/silk"

// applyOSCEPostSilk is a no-op outside of the explicit
// `gopus_unsupported_controls` build. The fanout call site in
// `streamState.decodeSILK` always invokes it so the shared code compiles on
// both builds; under the default tag it collapses to nothing.
func (d *streamState) applyOSCEPostSilk(_ []float32, _ int, _ silk.Bandwidth, _ bool) {
}
