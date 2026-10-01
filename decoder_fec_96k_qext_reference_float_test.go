//go:build gopus_qext && !gopus_fixed_point

package gopus

import "github.com/thesyncim/gopus/internal/libopustest"

func probeDecodeWithFEC96kOracle(p libopustest.QEXTDecode96kParams) (libopustest.QEXTDecode96kResult, error) {
	return libopustest.ProbeQEXTDecodeFECSequence(p)
}
