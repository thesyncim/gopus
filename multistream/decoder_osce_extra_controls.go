//go:build gopus_osce

package multistream

// OSCEModelsLoaded reports whether the retained blob contains the LACE and
// NoLACE OSCE model families.
func (d *Decoder) OSCEModelsLoaded() bool {
	return d.osceModelsLoaded
}

// OSCEBWEModelLoaded reports whether the retained blob contains the OSCE_BWE model family.
func (d *Decoder) OSCEBWEModelLoaded() bool {
	return d.osceBWEModelLoaded
}

// SetOSCEBWE stores tag-gated OSCE_BWE enable state and fans it out to every
// child stream decoder. libopus exposes the matching control via
// `OPUS_SET_DNN_BLOB` + `OPUS_SET_OSCE_METHOD` semantics by toggling
// `DecControl.enable_osce_bwe` on each per-stream decoder; the gopus
// multistream wiring mirrors that so an enabled control applies to every
// SILK-WB stream in the multistream packet.
func (d *Decoder) SetOSCEBWE(enabled bool) {
	d.osceBWEEnabled = enabled
	for _, dec := range d.decoders {
		if s, ok := dec.(*streamState); ok {
			s.setOSCEBWEEnabled(enabled)
		}
	}
}

// OSCEBWE reports the stored tag-gated OSCE_BWE enable state.
func (d *Decoder) OSCEBWE() bool {
	return d.osceBWEEnabled
}

// SetOSCELACE explicitly overrides the complexity-based OSCE LACE/NoLACE
// selection and fans the setting out to every child stream decoder. Without an
// explicit override, each child follows libopus and selects no method below
// complexity 6, LACE at 6, and NoLACE at 7 or above.
func (d *Decoder) SetOSCELACE(enabled bool) {
	d.osceLACEEnabled = enabled
	d.osceLACEOverrideSet = true
	for _, dec := range d.decoders {
		if s, ok := dec.(*streamState); ok {
			s.setOSCELACEEnabled(enabled)
		}
	}
}

// OSCELACE reports whether the OSCE LACE/NoLACE gate is enabled by the
// explicit override or by decoder complexity.
func (d *Decoder) OSCELACE() bool {
	if d == nil {
		return false
	}
	if st := d.firstStreamState(); st != nil {
		return st.osceLACEEnabledForComplexity()
	}
	return false
}
