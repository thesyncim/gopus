//go:build !amd64.v3 || gopus_fixed_point

package celt

const stereoMergeUsesFMA = false

func stereoMergeEnergy(mid, side, xp float32) (el, er float32) {
	mid2 := mid * mid
	return mid2 + side - float32(2)*xp, mid2 + side + float32(2)*xp
}
