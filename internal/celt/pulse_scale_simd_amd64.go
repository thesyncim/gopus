//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// scalePulsesInto sets out[i] = float32(pulses[i]) * g for every pulse, the
// normalise_residual() product loop. Four lanes convert and multiply
// exactly as the scalar loop does; the remainder runs scalar.
func scalePulsesInto(out []celtNorm, pulses []int32, g float32) {
	out = out[:len(pulses)]
	blocks := len(pulses) &^ 3
	if blocks > 0 {
		g4 := archsimd.BroadcastFloat32x4(g)
		pp := unsafe.Pointer(unsafe.SliceData(pulses))
		op := unsafe.Pointer(unsafe.SliceData(out))
		for j := 0; j < blocks; j += 4 {
			v := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(pp, 4*j))).ConvertToFloat32()
			storeF32x4(unsafe.Add(op, 4*j), v.Mul(g4))
		}
	}
	scalePulsesIntoScalar(out[blocks:], pulses[blocks:], g)
}
