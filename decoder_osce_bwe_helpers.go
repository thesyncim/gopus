//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
)

// bindOSCEBWEModel attaches (or detaches) the quarantined libopus OSCE BWE
// model to the decoder's runtime state. The runtime forward pass is still a
// Phase 1 stub; this helper only handles the typed model binding so callers
// can verify the loader recognises the upstream `bbwenet_*` weight records.
//
// supported reflects the blob's `SupportsOSCEBWE()` answer; when false the
// helper clears any prior binding.
func (d *Decoder) bindOSCEBWEModel(blob *dnnblob.Blob, supported bool) error {
	if d == nil {
		return nil
	}
	if blob == nil || !supported {
		if d.osceBWE != nil {
			d.osceBWE.osceBWEModel = nil
			_ = d.osceBWE.osceBWERuntime.SetModel(nil)
			d.osceBWE = nil
		}
		return nil
	}
	model, err := osceBWE.LoadModel(blob)
	if err != nil {
		// Keep d.osceBWEModelLoaded as the blob-level signal (still true) but
		// drop any prior runtime binding so callers see Loaded()==false.
		if d.osceBWE != nil {
			d.osceBWE.osceBWEModel = nil
			_ = d.osceBWE.osceBWERuntime.SetModel(nil)
			d.osceBWE = nil
		}
		return err
	}
	if d.osceBWE == nil {
		d.osceBWE = &decoderOSCEBWEState{}
	}
	d.osceBWE.osceBWEModel = model
	// Mirror the LPCNet pattern: keep the runtime state in sync with the
	// loaded model so a later Phase 2 forward pass can rely on Loaded().
	if err := d.osceBWE.osceBWERuntime.SetModel(blob); err != nil {
		d.osceBWE.osceBWEModel = nil
		d.osceBWE = nil
		return err
	}
	// Feature extractor state is independent of the model weights but its
	// signal-history / last-spec buffers must start from zero on (re)bind to
	// match `osce_init` in libopus.
	d.osceBWE.osceBWEFeatures.Reset()
	return nil
}

// osceBWEModelLoadedRuntime reports whether the decoder currently has a bound
// OSCE BWE model that the runtime can use. The bool mirrors the LPCNet
// `Loaded()` accessors and is intended for test parity assertions.
func (d *Decoder) osceBWEModelLoadedRuntime() bool {
	if d == nil || d.osceBWE == nil {
		return false
	}
	return d.osceBWE.osceBWEModel != nil && d.osceBWE.osceBWERuntime.Loaded()
}
