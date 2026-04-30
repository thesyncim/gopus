//go:build gopus_unsupported_controls || gopus_dred
// +build gopus_unsupported_controls gopus_dred

package multistream

import (
	"github.com/thesyncim/gopus/encoder"
	internaldred "github.com/thesyncim/gopus/internal/dred"
)

// DREDModelLoaded reports whether all stream encoders have a DRED-capable blob.
func (e *Encoder) DREDModelLoaded() bool {
	if len(e.encoders) == 0 {
		return false
	}
	return e.encoders[0].DREDModelLoaded()
}

// DREDReady reports whether all stream encoders are ready to emit DRED.
func (e *Encoder) DREDReady() bool {
	if len(e.encoders) == 0 {
		return false
	}
	return e.encoders[0].DREDReady()
}

// SetDREDDuration propagates libopus-style DRED duration to all stream encoders.
func (e *Encoder) SetDREDDuration(duration int) error {
	if duration < 0 || duration > internaldred.MaxFrames {
		return encoder.ErrInvalidDREDDuration
	}
	for _, enc := range e.encoders {
		if err := enc.SetDREDDuration(duration); err != nil {
			return err
		}
	}
	return nil
}

// DREDDuration reports the DRED duration from the first stream encoder.
func (e *Encoder) DREDDuration() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].DREDDuration()
	}
	return 0
}
