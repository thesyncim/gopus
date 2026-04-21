//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import encpkg "github.com/thesyncim/gopus/encoder"

// SetDREDDuration exposes the unsupported libopus ENABLE_DRED control only
// when built with -tags gopus_unsupported_controls.
//
// The default gopus build keeps this quarantined from the public API surface.
func (e *Encoder) SetDREDDuration(duration int) error {
	if err := e.enc.SetDREDDuration(duration); err != nil {
		if err == encpkg.ErrInvalidDREDDuration {
			return ErrInvalidArgument
		}
		return err
	}
	return nil
}

// DREDDuration reports encoder-side DRED redundancy depth for explicit
// quarantine builds.
func (e *Encoder) DREDDuration() (int, error) {
	return e.enc.DREDDuration(), nil
}
