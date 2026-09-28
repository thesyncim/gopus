//go:build gopus_qext && !gopus_fixed_point && !gopus_dred && !gopus_osce

package multistream

import "github.com/thesyncim/gopus/internal/libopustest"

func probeMultistreamNative96kReference(p libopustest.QEXTDecode96kParams) (libopustest.QEXTDecode96kResult, error) {
	return libopustest.ProbeQEXTDecode96k(p)
}
