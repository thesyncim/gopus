//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// imdctPreRotateFMA32Kiss is the archsimd IMDCT pre-rotation. Per output i it
// computes the complex rotation
//
//	x1 = spectrum[2i] (even, ascending); x2 = spectrum[n2-1-2i] (odd, descending)
//	fftIn[i] = (x1*t0 - x2*t1) + i(x2*t0 + x1*t1)
//
// The main loop handles three four-output vector groups at a time. It keeps
// group bases and uses fixed offsets within each group, which reduces loop
// address updates while preserving the rounded product and fused accumulate
// sequence. Remaining vector groups and scalar outputs use the same order.
func imdctPreRotateFMA32Kiss(fftIn []float32, spectrum []float32, trig []float32, n2, n4 int) {
	if n4 <= 0 {
		return
	}
	spectrum = spectrum[:n2]
	trig = trig[:n2]
	outF := fftIn[:2*n4]
	x1Off := 0
	x2Off := n2 - 1
	t0Off := 0
	t1Off := n4
	outOff := 0
	tailBlocks := n4 % 12 / 4
	for blocks := n4 / 12; blocks > 0; blocks-- {
		s1 := spectrum[x1Off : x1Off+24]
		s2 := spectrum[x2Off-23 : x2Off+1]
		t0 := trig[t0Off : t0Off+12]
		t1 := trig[t1Off : t1Off+12]
		o := outF[outOff : outOff+24]

		x1a := archsimd.LoadFloat32x4Array((*[4]float32)(s1[0:])).ToBits().
			ConcatEven(archsimd.LoadFloat32x4Array((*[4]float32)(s1[4:])).ToBits()).BitsToFloat32()
		x2a := reverse4(archsimd.LoadFloat32x4Array((*[4]float32)(s2[16:])).ToBits().
			ConcatOdd(archsimd.LoadFloat32x4Array((*[4]float32)(s2[20:])).ToBits()).BitsToFloat32())
		t0a := archsimd.LoadFloat32x4Array((*[4]float32)(t0[0:]))
		t1a := archsimd.LoadFloat32x4Array((*[4]float32)(t1[0:]))
		rea := x1a.MulAdd(t0a, x2a.Mul(t1a).Neg())
		ima := x2a.MulAdd(t0a, x1a.Mul(t1a))
		rea.ToBits().InterleaveLo(ima.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(o[0:]))
		rea.ToBits().InterleaveHi(ima.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(o[4:]))

		x1b := archsimd.LoadFloat32x4Array((*[4]float32)(s1[8:])).ToBits().
			ConcatEven(archsimd.LoadFloat32x4Array((*[4]float32)(s1[12:])).ToBits()).BitsToFloat32()
		x2b := reverse4(archsimd.LoadFloat32x4Array((*[4]float32)(s2[8:])).ToBits().
			ConcatOdd(archsimd.LoadFloat32x4Array((*[4]float32)(s2[12:])).ToBits()).BitsToFloat32())
		t0b := archsimd.LoadFloat32x4Array((*[4]float32)(t0[4:]))
		t1b := archsimd.LoadFloat32x4Array((*[4]float32)(t1[4:]))
		reb := x1b.MulAdd(t0b, x2b.Mul(t1b).Neg())
		imb := x2b.MulAdd(t0b, x1b.Mul(t1b))
		reb.ToBits().InterleaveLo(imb.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(o[8:]))
		reb.ToBits().InterleaveHi(imb.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(o[12:]))

		x1c := archsimd.LoadFloat32x4Array((*[4]float32)(s1[16:])).ToBits().
			ConcatEven(archsimd.LoadFloat32x4Array((*[4]float32)(s1[20:])).ToBits()).BitsToFloat32()
		x2c := reverse4(archsimd.LoadFloat32x4Array((*[4]float32)(s2[0:])).ToBits().
			ConcatOdd(archsimd.LoadFloat32x4Array((*[4]float32)(s2[4:])).ToBits()).BitsToFloat32())
		t0c := archsimd.LoadFloat32x4Array((*[4]float32)(t0[8:]))
		t1c := archsimd.LoadFloat32x4Array((*[4]float32)(t1[8:]))
		rec := x1c.MulAdd(t0c, x2c.Mul(t1c).Neg())
		imc := x2c.MulAdd(t0c, x1c.Mul(t1c))
		rec.ToBits().InterleaveLo(imc.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(o[16:]))
		rec.ToBits().InterleaveHi(imc.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(o[20:]))

		x1Off += 24
		x2Off -= 24
		t0Off += 12
		t1Off += 12
		outOff += 24
	}
	for blocks := tailBlocks; blocks > 0; blocks-- {
		s1 := spectrum[x1Off : x1Off+8]
		s2 := spectrum[x2Off-7 : x2Off+1]
		x1 := archsimd.LoadFloat32x4Array((*[4]float32)(s1[0:])).ToBits().
			ConcatEven(archsimd.LoadFloat32x4Array((*[4]float32)(s1[4:])).ToBits()).BitsToFloat32()
		oddAsc := archsimd.LoadFloat32x4Array((*[4]float32)(s2[0:])).ToBits().
			ConcatOdd(archsimd.LoadFloat32x4Array((*[4]float32)(s2[4:])).ToBits()).BitsToFloat32()
		x2 := reverse4(oddAsc)
		t0 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[t0Off:]))
		t1 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[t1Off:]))
		re := x1.MulAdd(t0, x2.Mul(t1).Neg())
		im := x2.MulAdd(t0, x1.Mul(t1))
		re.ToBits().InterleaveLo(im.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(outF[outOff:]))
		re.ToBits().InterleaveHi(im.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(outF[outOff+4:]))
		x1Off += 8
		x2Off -= 8
		t0Off += 4
		t1Off += 4
		outOff += 8
	}
	i := n4 - n4%4
	for ; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		outF[2*i] = opusmath.FMA32(x1, t0, -noFMA32Mul(x2, t1))
		outF[2*i+1] = opusmath.FMA32(x2, t0, noFMA32Mul(x1, t1))
	}
}
