//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

// SetDREDDuration exposes the unsupported libopus ENABLE_DRED control only
// when built with -tags gopus_unsupported_controls.
//
// The default gopus build keeps this quarantined from the public API surface.
// The current implementation still returns ErrUnsupportedExtension.
func (e *MultistreamEncoder) SetDREDDuration(_ int) error {
	return ErrUnsupportedExtension
}

// DREDDuration reports encoder-side DRED redundancy depth for explicit
// quarantine builds.
func (e *MultistreamEncoder) DREDDuration() (int, error) {
	return 0, ErrUnsupportedExtension
}
