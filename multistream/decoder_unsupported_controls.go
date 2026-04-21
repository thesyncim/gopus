//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package multistream

// DREDModelLoaded reports whether the retained blob contains the DRED decoder model family.
func (d *Decoder) DREDModelLoaded() bool {
	return d.dredModelLoaded
}

// OSCEModelsLoaded reports whether the retained blob contains the LACE and
// NoLACE OSCE model families.
func (d *Decoder) OSCEModelsLoaded() bool {
	return d.osceModelsLoaded
}

// OSCEBWEModelLoaded reports whether the retained blob contains the OSCE_BWE model family.
func (d *Decoder) OSCEBWEModelLoaded() bool {
	return d.osceBWEModelLoaded
}

// SetOSCEBWE stores tag-gated OSCE_BWE enable state for parity work.
func (d *Decoder) SetOSCEBWE(enabled bool) {
	d.osceBWEEnabled = enabled
}

// OSCEBWE reports the stored tag-gated OSCE_BWE enable state.
func (d *Decoder) OSCEBWE() bool {
	return d.osceBWEEnabled
}
