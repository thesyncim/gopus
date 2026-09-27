//go:build amd64 && goexperiment.simd && !nosimd

package rdovae

import "simd/archsimd"

var rdovaeX86Enabled = archsimd.X86.AVX2() && archsimd.X86.FMA()

// dnn/vec_avx.h:sgemv accumulates each output lane through ascending-column
// AVX2 FMA chains for complete 16-, 8-, and 4-row blocks.
func sgemvX86Fused(out []float32, weights FloatTensor, rows, cols, colStride int, x []float32) {
	row := 0
	for ; row+16 <= rows; row += 16 {
		var acc0, acc1 archsimd.Float32x8
		var w0, w1 [8]float32
		for col := 0; col < cols; col++ {
			base := col*colStride + row
			for k := range w0 {
				w0[k] = weights.At(base + k)
				w1[k] = weights.At(base + 8 + k)
			}
			v := archsimd.BroadcastFloat32x8(x[col])
			acc0 = archsimd.LoadFloat32x8Array(&w0).MulAdd(v, acc0)
			acc1 = archsimd.LoadFloat32x8Array(&w1).MulAdd(v, acc1)
		}
		acc0.Store(out[row:])
		acc1.Store(out[row+8:])
	}
	for ; row+8 <= rows; row += 8 {
		var acc archsimd.Float32x8
		var w [8]float32
		for col := 0; col < cols; col++ {
			base := col*colStride + row
			for k := range w {
				w[k] = weights.At(base + k)
			}
			acc = archsimd.LoadFloat32x8Array(&w).MulAdd(archsimd.BroadcastFloat32x8(x[col]), acc)
		}
		acc.Store(out[row:])
	}
	for ; row+4 <= rows; row += 4 {
		var acc archsimd.Float32x4
		var w [4]float32
		for col := 0; col < cols; col++ {
			base := col*colStride + row
			for k := range w {
				w[k] = weights.At(base + k)
			}
			acc = archsimd.LoadFloat32x4Array(&w).MulAdd(archsimd.BroadcastFloat32x4(x[col]), acc)
		}
		acc.Store(out[row:])
	}
	for ; row < rows; row++ {
		var sum float32
		for col := 0; col < cols; col++ {
			sum += weights.At(col*colStride+row) * x[col]
		}
		out[row] = sum
	}
}

// dnn/vec_avx.h:sparse_sgemv8x4 keeps eight output lanes live while applying
// each four-column block through four ascending AVX2 FMA operations.
func sparseSGEMVX86Fused(out []float32, weights FloatTensor, idx IntTensor, x []float32) {
	weightOffset, idxPos := 0, 0
	for row := 0; row < len(out); row += 8 {
		var acc archsimd.Float32x8
		var w [8]float32
		blocks := int(idx.At(idxPos))
		idxPos++
		for range blocks {
			col := int(idx.At(idxPos))
			idxPos++
			for j := 0; j < 4; j++ {
				for k := range w {
					w[k] = weights.At(weightOffset + j*8 + k)
				}
				acc = archsimd.LoadFloat32x8Array(&w).MulAdd(archsimd.BroadcastFloat32x8(x[col+j]), acc)
			}
			weightOffset += SparseBlockSize
		}
		acc.Store(out[row:])
	}
}
