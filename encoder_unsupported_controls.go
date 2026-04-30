//go:build gopus_unsupported_controls || gopus_dred
// +build gopus_unsupported_controls gopus_dred

package gopus

import encpkg "github.com/thesyncim/gopus/encoder"

// SetDREDDuration exposes the libopus ENABLE_DRED control when built with
// -tags gopus_dred, or for quarantine parity work under
// -tags gopus_unsupported_controls.
//
// The default gopus build keeps this absent from the public API surface.
func (e *Encoder) SetDREDDuration(duration int) error {
	if err := e.enc.SetDREDDuration(duration); err != nil {
		if err == encpkg.ErrInvalidDREDDuration {
			return ErrInvalidArgument
		}
		return err
	}
	return nil
}

// DREDDuration reports encoder-side DRED redundancy depth for tagged builds.
func (e *Encoder) DREDDuration() (int, error) {
	return e.enc.DREDDuration(), nil
}
