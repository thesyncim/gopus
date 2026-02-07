//go:build cgo_libopus

package testvectors

import cgowrap "github.com/thesyncim/gopus/celt/cgo_test"

func libopusQuantizeGainsVector(gainsQ16 []int32, prevInd int8, conditional bool, nbSubfr int) ([]int8, int8, bool) {
	indices, _, prevOut := cgowrap.GainQuantizeVector(gainsQ16, prevInd, conditional, nbSubfr)
	return indices, prevOut, true
}
