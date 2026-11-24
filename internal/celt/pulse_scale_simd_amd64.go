//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// scalePulsesInto sets out[i] = float32(pulses[i]) * g for every pulse, the
// normalise_residual() product loop. Four lanes convert and multiply
// exactly as the scalar loop does; the remainder runs scalar.
func scalePulsesInto(out []celtNorm, pulses []int32, g float32) {
	out = out[:len(pulses)]
	if !archsimd.X86.AVX() {
		scalePulsesIntoScalar(out, pulses, g)
		return
	}
	scalePulsesIntoAVX(out, pulses, g)
}

//go:noinline
func scalePulsesIntoAVX(out []celtNorm, pulses []int32, g float32) {
	out = out[:len(pulses)]
	blocks := len(pulses) &^ 3
	if blocks > 0 {
		g4 := broadcastF32x4Arch(g)
		pb := pulses[:blocks]
		ob := out[:blocks]
		for j := 0; j < len(pb); j += 4 {
			v := archsimd.LoadInt32x4Array((*[4]int32)(pb[j:])).ConvertToFloat32()
			v.Mul(g4).StoreArray((*[4]float32)(ob[j:]))
		}
	}
	scalePulsesIntoScalar(out[blocks:], pulses[blocks:], g)
}
