//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

// SetOSCEBWE exposes the unsupported libopus ENABLE_OSCE_BWE control only
// when built with -tags gopus_unsupported_controls.
//
// The default gopus build keeps this quarantined from the public API surface.
func (d *Decoder) SetOSCEBWE(enabled bool) error {
	d.osceBWEEnabled = enabled
	return nil
}

// OSCEBWE reports decoder-side OSCE bandwidth-extension state for explicit
// quarantine builds.
func (d *Decoder) OSCEBWE() (bool, error) {
	return d.osceBWEEnabled, nil
}
