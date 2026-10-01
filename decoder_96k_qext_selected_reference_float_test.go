//go:build gopus_qext && !gopus_fixed_point

package gopus_test

import "github.com/thesyncim/gopus/internal/libopustest"

func probeQEXTDecodePublicReference(p libopustest.QEXTDecode96kParams) (libopustest.QEXTDecode96kResult, error) {
	return libopustest.ProbeQEXTDecode96k(p)
}
