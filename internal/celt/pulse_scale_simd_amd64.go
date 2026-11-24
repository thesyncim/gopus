//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

var pulseScaleUsesAVX2 = archsimd.X86.AVX2()

// scalePulsesInto sets out[i] = float32(pulses[i]) * g for every pulse, the
// normalise_residual() product loop. Eight lanes on AVX2 (or four on AVX1)
// convert and multiply exactly as the scalar loop does; the remainder runs
// scalar.
func scalePulsesInto(out []celtNorm, pulses []int32, g float32) {
	out = out[:len(pulses)]
	if pulseScaleUsesAVX2 && len(pulses) >= 8 {
		scalePulsesIntoAVX2(out, pulses, g)
		return
	}
	if !archsimd.X86.AVX() {
		scalePulsesIntoScalar(out, pulses, g)
		return
	}
	scalePulsesIntoAVX(out, pulses, g)
}

//go:noinline
func scalePulsesIntoAVX2(out []celtNorm, pulses []int32, g float32) {
	n := len(pulses)
	out = out[:n]
	g8 := archsimd.BroadcastFloat32x8(g)
	j := 0
	for ; j+8 <= n; j += 8 {
		v := archsimd.LoadInt32x8Array((*[8]int32)(pulses[j:])).ConvertToFloat32()
		v.Mul(g8).StoreArray((*[8]float32)(out[j:]))
	}
	archsimd.ClearAVXUpperBits()
	if j+4 <= n {
		g4 := broadcastF32x4Arch(g)
		v := archsimd.LoadInt32x4Array((*[4]int32)(pulses[j:])).ConvertToFloat32()
		v.Mul(g4).StoreArray((*[4]float32)(out[j:]))
		j += 4
	}
	scalePulsesIntoScalar(out[j:], pulses[j:], g)
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
