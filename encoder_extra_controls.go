//go:build gopus_osce || gopus_dred

package gopus

import encpkg "github.com/thesyncim/gopus/internal/encoder"

// SetDREDDuration sets the maximum number of 10 ms DRED redundancy frames. Values from
// 0 through 104 are accepted; zero disables DRED emission. This control is
// available in builds tagged gopus_dred or gopus_osce.
func (e *Encoder) SetDREDDuration(duration int) error {
	if err := e.enc.SetDREDDuration(duration); err != nil {
		if err == encpkg.ErrInvalidDREDDuration {
			return ErrInvalidArgument
		}
		return err
	}
	return nil
}

// DREDDuration reports the configured DRED redundancy depth in 10 ms frames;
// zero means DRED emission is disabled.
func (e *Encoder) DREDDuration() (int, error) {
	return e.enc.DREDDuration(), nil
}
