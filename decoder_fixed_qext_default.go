//go:build !gopus_fixed_point || !gopus_qext

package gopus

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func (d *Decoder) decodeFixedQEXTCELTFrame(_ *rangecoding.Decoder, _, _ int, _ bool, _ celt.CELTBandwidth, _ []byte, _ bool) (bool, error) {
	return false, nil
}

func (d *Decoder) resetFixedQEXTCELT() {}

func (d *Decoder) invalidateFixedQEXTCELT() {}

func (d *Decoder) setFixedQEXTPhaseInversionDisabled(bool) {}
