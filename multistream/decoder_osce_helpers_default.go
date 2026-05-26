//go:build !gopus_extra_controls

package multistream

import "github.com/thesyncim/gopus/internal/dnnblob"

// setOSCELACEEnabled / setOSCEBWEEnabled / bindOSCEModels are no-ops outside
// of the explicit `gopus_extra_controls` build. The fanout call sites in
// the multistream Decoder always invoke them so the shared code compiles on
// both builds; under the default tag they collapse to nothing.

func (d *streamState) setOSCELACEEnabled(_ bool) {}

func (d *streamState) setOSCEBWEEnabled(_ bool) {}

func (d *streamState) bindOSCEModels(_ *dnnblob.Blob) error { return nil }

func (d *streamState) resetOSCEPostfilterState() {}
