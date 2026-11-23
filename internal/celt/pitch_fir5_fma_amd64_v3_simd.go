//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import "simd/archsimd"

const celtFIR5UsesFMA = true

// celtFIR5Accumulate follows the fused MAC sequence in the AMD64 v3
// libopus celt_fir5 caller.
func celtFIR5Accumulate(sum, coefficient, previous archsimd.Float32x4) archsimd.Float32x4 {
	return coefficient.MulAdd(previous, sum)
}

// celtFIR5Head filters the unprocessed prefix using the same five fused MACs
// per output as celt_fir5. celtFIR5AVX leaves at most eight prefix samples;
// the local window preserves them while overlapping vectors are evaluated.
func celtFIR5Head(x []float32, num [5]float32) {
	if len(x) == 0 {
		return
	}

	var memory [13]float32
	copy(memory[5:], x)
	n0 := broadcastF32x4Arch(num[0])
	n1 := broadcastF32x4Arch(num[1])
	n2 := broadcastF32x4Arch(num[2])
	n3 := broadcastF32x4Arch(num[3])
	n4 := broadcastF32x4Arch(num[4])

	first := celtFIR5HeadVector(&memory, 0, n0, n1, n2, n3, n4)
	var out [4]float32
	first.StoreArray(&out)
	copy(x, out[:min(len(x), len(out))])

	if len(x) > 4 {
		start := len(x) - 4
		tail := celtFIR5HeadVector(&memory, start, n0, n1, n2, n3, n4)
		tail.StoreArray(&out)
		copy(x[start:], out[:])
	}
}

func celtFIR5HeadVector(memory *[13]float32, start int, n0, n1, n2, n3, n4 archsimd.Float32x4) archsimd.Float32x4 {
	w := memory[start : start+9 : start+9]
	sum := archsimd.LoadFloat32x4Array((*[4]float32)(w[5:9]))
	sum = celtFIR5Accumulate(sum, n0, archsimd.LoadFloat32x4Array((*[4]float32)(w[4:8])))
	sum = celtFIR5Accumulate(sum, n1, archsimd.LoadFloat32x4Array((*[4]float32)(w[3:7])))
	sum = celtFIR5Accumulate(sum, n2, archsimd.LoadFloat32x4Array((*[4]float32)(w[2:6])))
	sum = celtFIR5Accumulate(sum, n3, archsimd.LoadFloat32x4Array((*[4]float32)(w[1:5])))
	sum = celtFIR5Accumulate(sum, n4, archsimd.LoadFloat32x4Array((*[4]float32)(w[0:4])))
	return sum
}
