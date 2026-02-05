//go:build !cgo_libopus

package testvectors

func libopusQuantizeGainsVector(_ []int32, _ int8, _ bool, _ int) ([]int8, int8, bool) {
	return nil, 0, false
}
