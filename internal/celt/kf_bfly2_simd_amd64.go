//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// kfBfly2M4SIMD is kfBfly2M4Scalar with the four butterflies of each group
// held in real and imaginary vectors. Each lane keeps the scalar operand order,
// and the lanes that take no twiddle product pick their inputs with blends.
func kfBfly2M4SIMD(fout []float32, N int) {
	if N <= 0 {
		return
	}
	if !archsimd.X86.AVX() {
		kfBfly2M4Scalar(fout, N)
		return
	}
	ff := fout[:16*N]
	tw := broadcastF32x4Arch(kfBfly2M4Twiddle)
	lane0 := archsimd.LoadInt32x4Array(&[4]int32{-1, 0, 0, 0}).ToMask()
	lane1 := archsimd.LoadInt32x4Array(&[4]int32{0, -1, 0, 0}).ToMask()
	lane2 := archsimd.LoadInt32x4Array(&[4]int32{0, 0, -1, 0}).ToMask()
	lane3 := archsimd.LoadInt32x4Array(&[4]int32{0, 0, 0, -1}).ToMask()
	fusedGroups := N &^ 3
	for group := 0; group < N; group++ {
		off := group * 16
		fr, fi := bflyLoadCpx4AMD64(ff[off:])
		r, i := bflyLoadCpx4AMD64(ff[off+8:])
		minusR, minusI, plusR, plusI := bfly2M4GroupOutputsAMD64(fr, fi, r, i, tw, lane0, lane1, lane2, lane3, group < fusedGroups)
		bflyStoreCpx4AMD64(ff[off+8:], minusR, minusI)
		bflyStoreCpx4AMD64(ff[off:], plusR, plusI)
	}
}
