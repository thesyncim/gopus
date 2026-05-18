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

// SetOSCELACE exposes the unsupported libopus OSCE LACE/NoLACE postfilter
// activation control only when built with -tags gopus_unsupported_controls.
//
// The default gopus build keeps this quarantined from the public API surface.
// libopus selects between OSCE_METHOD_NONE / OSCE_METHOD_LACE / OSCE_METHOD_NOLACE
// based on encoder complexity (>=6 enables LACE, >=7 enables NoLACE); this
// boolean control gates whether the gopus decoder runs either postfilter on
// the SILK lowband output before the silk_resampler / OSCE BWE stages.
func (d *Decoder) SetOSCELACE(enabled bool) error {
	d.osceLACEEnabled = enabled
	return nil
}

// OSCELACE reports decoder-side OSCE LACE/NoLACE postfilter activation state
// for explicit quarantine builds.
func (d *Decoder) OSCELACE() (bool, error) {
	return d.osceLACEEnabled, nil
}
