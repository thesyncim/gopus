//go:build amd64 && goexperiment.simd && !nosimd && !purego

package dnnmath

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"simd/archsimd"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

// The references below gather every weight through the typed view accessors
// and apply the same vector operations; the kernels load the blob bytes
// directly and must produce identical bits.

func refSGEMVX86(out []float32, weights dnnblob.Float32View, rows, cols, colStride int, x []float32) {
	row := 0
	for ; row+8 <= rows; row += 8 {
		var acc archsimd.Float32x8
		for col := range cols {
			var w [8]float32
			for k := range w {
				w[k] = weights.At(col*colStride + row + k)
			}
			acc = archsimd.LoadFloat32x8Array(&w).MulAdd(archsimd.BroadcastFloat32x8(x[col]), acc)
		}
		acc.Store(out[row:])
	}
	for ; row+4 <= rows; row += 4 {
		var acc archsimd.Float32x4
		for col := range cols {
			var w [4]float32
			for k := range w {
				w[k] = weights.At(col*colStride + row + k)
			}
			acc = archsimd.LoadFloat32x4Array(&w).MulAdd(archsimd.BroadcastFloat32x4(x[col]), acc)
		}
		acc.Store(out[row:])
	}
	for ; row < rows; row++ {
		var sum float32
		for col := range cols {
			sum += float32(weights.At(col*colStride+row) * x[col])
		}
		out[row] = sum
	}
	archsimd.ClearAVXUpperBits()
}

func refSparseSGEMV8x4X86(out []float32, weights dnnblob.Float32View, idx dnnblob.Int32View, rows int, x []float32) {
	wOffset, idxPos := 0, 0
	for row := 0; row < rows; row += 8 {
		var acc archsimd.Float32x8
		colBlocks := int(idx.At(idxPos))
		idxPos++
		for range colBlocks {
			pos := int(idx.At(idxPos))
			idxPos++
			for tap := range 4 {
				var w [8]float32
				for k := range w {
					w[k] = weights.At(wOffset + 8*tap + k)
				}
				acc = archsimd.LoadFloat32x8Array(&w).MulAdd(archsimd.BroadcastFloat32x8(x[pos+tap]), acc)
			}
			wOffset += 32
		}
		acc.Store(out[row:])
	}
	archsimd.ClearAVXUpperBits()
}

func refCGEMVBlock(acc archsimd.Int32x8, q []uint8, col int, weights dnnblob.Int8View, wOffset int) archsimd.Int32x8 {
	var w [32]int8
	for k := range w {
		w[k] = weights.At(wOffset + k)
	}
	packed := uint32(q[col]) | uint32(q[col+1])<<8 | uint32(q[col+2])<<16 | uint32(q[col+3])<<24
	pairs := archsimd.BroadcastUint32x8(packed).AsUint8x32().DotProductPairsSaturated(archsimd.LoadInt8x32Array(&w))
	return acc.Add(pairs.DotProductPairs(archsimd.BroadcastInt16x16(1)))
}

func refStoreScaled(out []float32, row int, acc archsimd.Int32x8, scale dnnblob.Float32View) {
	var s [8]float32
	for k := range s {
		s[k] = scale.At(row + k)
	}
	acc.ConvertToFloat32().Mul(archsimd.LoadFloat32x8Array(&s)).Store(out[row:])
}

func refCGEMV8x4X86(out []float32, weights dnnblob.Int8View, idx dnnblob.Int32View, sparse bool, scale dnnblob.Float32View, rows, cols int, x []float32, q []uint8) {
	quantizeInputX86(q, x, cols)
	wOffset, idxPos := 0, 0
	for row := 0; row < rows; row += 8 {
		var acc archsimd.Int32x8
		if sparse {
			colBlocks := int(idx.At(idxPos))
			idxPos++
			for range colBlocks {
				col := int(idx.At(idxPos))
				idxPos++
				acc = refCGEMVBlock(acc, q, col, weights, wOffset)
				wOffset += 32
			}
		} else {
			for col := 0; col < cols; col += 4 {
				acc = refCGEMVBlock(acc, q, col, weights, wOffset)
				wOffset += 32
			}
		}
		refStoreScaled(out, row, acc, scale)
	}
	archsimd.ClearAVXUpperBits()
}

func refConv2D3x3X86(out []float32, weights dnnblob.Float32View, inChannels, outChannels int, in []float32, height, hstride int) {
	inStride := height + 2
	for i := range outChannels {
		o := out[i*hstride : i*hstride+height]
		clear(o)
		for m := range inChannels {
			var w [9]archsimd.Float32x8
			for k := range w {
				w[k] = archsimd.BroadcastFloat32x8(weights.At((i*inChannels+m)*9 + k))
			}
			r0 := in[m*inStride:]
			r1 := in[(inChannels+m)*inStride:]
			r2 := in[(2*inChannels+m)*inStride:]
			for j := range height {
				acc := w[1].Mul(archsimd.BroadcastFloat32x8(r0[j+1]))
				acc = w[0].MulAdd(archsimd.BroadcastFloat32x8(r0[j]), acc)
				acc = w[2].MulAdd(archsimd.BroadcastFloat32x8(r0[j+2]), acc)
				acc = w[3].MulAdd(archsimd.BroadcastFloat32x8(r1[j]), acc)
				acc = w[4].MulAdd(archsimd.BroadcastFloat32x8(r1[j+1]), acc)
				acc = w[5].MulAdd(archsimd.BroadcastFloat32x8(r1[j+2]), acc)
				acc = w[6].MulAdd(archsimd.BroadcastFloat32x8(r2[j]), acc)
				acc = w[7].MulAdd(archsimd.BroadcastFloat32x8(r2[j+1]), acc)
				acc = w[8].MulAdd(archsimd.BroadcastFloat32x8(r2[j+2]), acc)
				o[j] = acc.GetLo().GetElem(0) + o[j]
			}
		}
	}
	archsimd.ClearAVXUpperBits()
}

func equivFloat(r *rand.Rand) float32 {
	switch r.IntN(24) {
	case 0:
		return float32(math.Copysign(0, -1))
	case 1:
		return 0
	case 2:
		return float32(math.NaN())
	case 3:
		return float32(math.Inf(1 - 2*r.IntN(2)))
	case 4:
		return math.Float32frombits(r.Uint32() & 0x807fffff) // subnormal
	}
	return float32(r.NormFloat64())
}

func equivFloatBlob(t *testing.T, r *rand.Rand, n int) (dnnblob.Float32View, []float32) {
	vals := make([]float32, n)
	b := make([]byte, 4*n)
	for i := range vals {
		vals[i] = equivFloat(r)
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(vals[i]))
	}
	v, err := dnnblob.Float32ViewFromBytes(b, int32(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	return v, vals
}

func equalBits(t *testing.T, name string, got, want []float32) {
	t.Helper()
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d] = 0x%08x want 0x%08x", name, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func TestX86DNNBlobKernelsMatchViewGather(t *testing.T) {
	if !X86VectorKernels {
		t.Skip("AVX2/FMA DNN kernels require AVX2 and FMA")
	}
	r := rand.New(rand.NewPCG(13, 1))
	for iter := range 300 {
		rows := 1 + r.IntN(70)
		cols := 1 + r.IntN(40)
		colStride := rows + r.IntN(5)
		w, _ := equivFloatBlob(t, r, cols*colStride+8)
		x := make([]float32, cols)
		for i := range x {
			x[i] = equivFloat(r)
		}
		got := make([]float32, rows)
		want := make([]float32, rows)
		SGEMVX86(got, w, rows, cols, colStride, x)
		refSGEMVX86(want, w, rows, cols, colStride, x)
		equalBits(t, "SGEMVX86", got, want)

		// Sparse float and int8 layouts: 8-row blocks of 4-column groups.
		rows8 := 8 * (1 + r.IntN(6))
		cols4 := 4 * (1 + r.IntN(10))
		var idx []int32
		blocks := 0
		for range rows8 / 8 {
			n := r.IntN(cols4/4 + 1)
			idx = append(idx, int32(n))
			for range n {
				idx = append(idx, int32(4*r.IntN(cols4/4)))
			}
			blocks += n
		}
		ib := make([]byte, 4*len(idx))
		for i, v := range idx {
			binary.LittleEndian.PutUint32(ib[4*i:], uint32(v))
		}
		iv, err := dnnblob.Int32ViewFromBytes(ib, int32(len(ib)))
		if err != nil {
			t.Fatal(err)
		}
		sw, _ := equivFloatBlob(t, r, 32*blocks)
		xs := make([]float32, cols4)
		for i := range xs {
			xs[i] = equivFloat(r)
		}
		got = make([]float32, rows8)
		want = make([]float32, rows8)
		SparseSGEMV8x4X86(got, sw, iv, rows8, xs)
		refSparseSGEMV8x4X86(want, sw, iv, rows8, xs)
		equalBits(t, "SparseSGEMV8x4X86", got, want)

		scale, _ := equivFloatBlob(t, r, rows8)
		for i := range xs {
			xs[i] = float32(r.Float64()*2.4 - 1.2)
		}
		dense := make([]byte, rows8*cols4)
		sparse := make([]byte, 32*blocks)
		for _, b := range [][]byte{dense, sparse} {
			for i := range b {
				b[i] = byte(r.Uint32())
			}
		}
		dv, _ := dnnblob.Int8ViewFromBytes(dense, int32(len(dense)))
		spv, _ := dnnblob.Int8ViewFromBytes(sparse, int32(len(sparse)))
		q := make([]uint8, cols4)
		CGEMV8x4X86(got, dv, scale, rows8, cols4, xs, q)
		refCGEMV8x4X86(want, dv, dnnblob.Int32View{}, false, scale, rows8, cols4, xs, q)
		equalBits(t, "CGEMV8x4X86", got, want)
		SparseCGEMV8x4X86(got, spv, iv, scale, rows8, cols4, xs, q)
		refCGEMV8x4X86(want, spv, iv, true, scale, rows8, cols4, xs, q)
		equalBits(t, "SparseCGEMV8x4X86", got, want)

		if iter%3 == 0 {
			inCh, outCh := 1+r.IntN(3), 1+r.IntN(3)
			height := 1 + r.IntN(20)
			hstride := height + r.IntN(4)
			cw, _ := equivFloatBlob(t, r, 9*inCh*outCh)
			in := make([]float32, 3*inCh*(height+2))
			for i := range in {
				in[i] = equivFloat(r)
			}
			got = make([]float32, outCh*hstride)
			want = make([]float32, outCh*hstride)
			Conv2D3x3X86(got, cw, inCh, outCh, in, height, hstride)
			refConv2D3x3X86(want, cw, inCh, outCh, in, height, hstride)
			equalBits(t, "Conv2D3x3X86", got, want)
		}
	}
}

func TestX86SGEMVAcceptsMisalignedBlobPayload(t *testing.T) {
	if !X86VectorKernels {
		t.Skip("AVX2/FMA DNN kernels require AVX2 and FMA")
	}

	const rows, cols, colStride = 29, 3, 32
	r := rand.New(rand.NewPCG(13, 3))
	backing := make([]byte, 1+4*cols*colStride)
	raw := backing[1:]
	for i := range cols * colStride {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(equivFloat(r)))
	}
	weights, err := dnnblob.Float32ViewFromBytes(raw, int32(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	x := make([]float32, cols)
	for i := range x {
		x[i] = equivFloat(r)
	}
	got, want := make([]float32, rows), make([]float32, rows)
	SGEMVX86(got, weights, rows, cols, colStride, x)
	refSGEMVX86(want, weights, rows, cols, colStride, x)
	equalBits(t, "SGEMVX86 misaligned payload", got, want)
	if allocs := testing.AllocsPerRun(100, func() {
		SGEMVX86(got, weights, rows, cols, colStride, x)
	}); allocs != 0 {
		t.Fatalf("SGEMVX86 allocations = %g", allocs)
	}
}

func TestX86DNNActivationTailsMatchVectorLanes(t *testing.T) {
	if !X86VectorKernels {
		t.Skip("AVX2/FMA DNN kernels require AVX2 and FMA")
	}
	r := rand.New(rand.NewPCG(13, 2))
	in := make([]float32, 23)
	out := make([]float32, len(in))
	for range 200 {
		for i := range in {
			in[i] = equivFloat(r) * 4
		}
		for _, k := range []struct {
			name string
			vec  func(out, in []float32, n int)
			lane func(archsimd.Float32x8) archsimd.Float32x8
		}{
			{"sigmoid", sigmoidVectorX86, sigmoid8X86},
			{"tanh", tanhVectorX86, tanh8X86},
			{"exp", expVectorX86, exp8X86},
		} {
			k.vec(out, in, len(in))
			want := make([]float32, len(in))
			for i, v := range in {
				want[i] = k.lane(archsimd.BroadcastFloat32x8(v)).GetLo().GetElem(0)
			}
			archsimd.ClearAVXUpperBits()
			equalBits(t, k.name, out, want)
		}
	}
}
