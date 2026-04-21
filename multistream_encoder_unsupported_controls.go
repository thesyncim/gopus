//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

// SetDREDDuration exposes the unsupported libopus ENABLE_DRED control only
// when built with -tags gopus_unsupported_controls.
//
// The default gopus build keeps this quarantined from the public API surface.
func (e *MultistreamEncoder) SetDREDDuration(duration int) error {
	if err := e.enc.SetDREDDuration(duration); err != nil {
		return ErrInvalidArgument
	}
	return nil
}

// DREDDuration reports encoder-side DRED redundancy depth for explicit
// quarantine builds.
func (e *MultistreamEncoder) DREDDuration() (int, error) {
	return e.enc.DREDDuration(), nil
}
