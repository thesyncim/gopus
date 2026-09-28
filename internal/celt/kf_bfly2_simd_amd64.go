//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"unsafe"

	"simd/archsimd"
)

// kfBfly2M4SIMD is kfBfly2M4Scalar with the four butterflies of each group
// held in real and imaginary vectors. Each lane keeps the scalar operand order,
// and the lanes that take no twiddle product pick their inputs with blends.
func kfBfly2M4SIMD(fout []kissCpx, N int) {
	if N <= 0 {
		return
	}
	if !archsimd.X86.AVX() {
		kfBfly2M4Scalar(fout, N)
		return
	}
	_ = fout[8*N-1]
	p := unsafe.Pointer(unsafe.SliceData(fout))
	tw := broadcastF32x4Arch(kfBfly2M4Twiddle)
	lane0 := archsimd.LoadInt32x4Array(&[4]int32{-1, 0, 0, 0}).ToMask()
	lane1 := archsimd.LoadInt32x4Array(&[4]int32{0, -1, 0, 0}).ToMask()
	lane2 := archsimd.LoadInt32x4Array(&[4]int32{0, 0, -1, 0}).ToMask()
	for range N {
		fr, fi := bflyLoadCpx4AMD64(p)
		r, i := bflyLoadCpx4AMD64(unsafe.Add(p, 32))
		sumRI := r.Add(i).Mul(tw) // lane 1: (fout2[1].r + fout2[1].i) * tw
		difIR := i.Sub(r).Mul(tw) // lane 1: (fout2[1].i - fout2[1].r) * tw; lane 3 likewise
		sumIR := i.Add(r).Mul(tw) // lane 3: (fout2[3].i + fout2[3].r) * tw
		tr := r.IfElse(lane0, sumRI.IfElse(lane1, i.IfElse(lane2, difIR)))
		ti := i.IfElse(lane0, difIR.IfElse(lane1, negF32x4AVX(r).IfElse(lane2, negF32x4AVX(sumIR))))
		bflyStoreCpx4AMD64(unsafe.Add(p, 32), fr.Sub(tr), fi.Sub(ti))
		bflyStoreCpx4AMD64(p, fr.Add(tr), fi.Add(ti))
		p = unsafe.Add(p, 64)
	}
}
