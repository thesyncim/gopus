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
//
// Both per-channel runtime slots are bound to the same model so that stereo
// SILK WB decode paths can call `osceBWERuntime[0].Process` for the mid/left
// channel and `osceBWERuntime[1].Process` for the side/right channel without
// extra plumbing. libopus does the same: a single `osce_model` plus one
// `silk_OSCE_BWE_struct` per channel state.
func (d *Decoder) bindOSCEBWEModel(blob *dnnblob.Blob, supported bool) error {
	if d == nil {
		return nil
	}
	if blob == nil || !supported {
		if d.osceBWE != nil {
			d.osceBWE.osceBWEModel = nil
			for ch := range d.osceBWE.osceBWERuntime {
				_ = d.osceBWE.osceBWERuntime[ch].SetModel(nil)
			}
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
			for ch := range d.osceBWE.osceBWERuntime {
				_ = d.osceBWE.osceBWERuntime[ch].SetModel(nil)
			}
			d.osceBWE = nil
		}
		return err
	}
	if d.osceBWE == nil {
		d.osceBWE = &decoderOSCEBWEState{}
	}
	d.osceBWE.osceBWEModel = model
	// Mirror the LPCNet pattern: keep both runtime states in sync with the
	// loaded model so a later Phase 2 forward pass can rely on Loaded() for
	// each channel slot independently.
	for ch := range d.osceBWE.osceBWERuntime {
		if err := d.osceBWE.osceBWERuntime[ch].SetModel(blob); err != nil {
			d.osceBWE.osceBWEModel = nil
			// Clear any sibling slot we may have already bound so the
			// runtime state is fully detached on failure.
			for j := range d.osceBWE.osceBWERuntime {
				_ = d.osceBWE.osceBWERuntime[j].SetModel(nil)
			}
			d.osceBWE = nil
			return err
		}
	}
	// Feature extractor state is independent of the model weights but its
	// signal-history / last-spec buffers must start from zero on (re)bind to
	// match `osce_init` in libopus.
	d.osceBWE.osceBWEFeatures[0].Reset()
	d.osceBWE.osceBWEFeatures[1].Reset()
	return nil
}

// osceBWEModelLoadedRuntime reports whether the decoder currently has a bound
// OSCE BWE model that the runtime can use. The bool mirrors the LPCNet
// `Loaded()` accessors and is intended for test parity assertions.
//
// Stereo SILK WB requires both per-channel runtime slots to be bound; the
// helper accordingly returns true only when slot 0 (mid/left) is loaded,
// matching the mono gate. Callers that care specifically about the side
// channel runtime can introspect `d.osceBWE.osceBWERuntime[1].Loaded()`.
func (d *Decoder) osceBWEModelLoadedRuntime() bool {
	if d == nil || d.osceBWE == nil {
		return false
	}
	return d.osceBWE.osceBWEModel != nil && d.osceBWE.osceBWERuntime[0].Loaded()
}

// osceBWEModelLoadedRuntimeAllChannels reports whether both per-channel
// runtime slots are bound. Stereo decode paths gate on this to ensure the
// side-channel forward pass has a valid model binding.
func (d *Decoder) osceBWEModelLoadedRuntimeAllChannels() bool {
	if d == nil || d.osceBWE == nil || d.osceBWE.osceBWEModel == nil {
		return false
	}
	for ch := range d.osceBWE.osceBWERuntime {
		if !d.osceBWE.osceBWERuntime[ch].Loaded() {
			return false
		}
	}
	return true
}

// Compile-time sanity: keep the runtime alias visible so we don't drop the
// dependency when refactoring the slot count.
var _ = (*osceBWE.State)(nil)
