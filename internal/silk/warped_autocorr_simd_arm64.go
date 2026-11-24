//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

// warpedAutocorrelationSections runs silk_warped_autocorrelation_FLP four
// input samples at a time as an ARM64 NEON Float64x2 wavefront, matching
// warpedAutocorrelationSamples bit-for-bit in every state and correlation entry.
func warpedAutocorrelationSections(st, corr *warpedAutocorrState, in []float32, w silkCReal, order int) {
	n := warpedAutocorrelationSectionsNEON(st, corr, in, w, order)
	warpedAutocorrelationSamples(st, corr, in[n:], w, order)
}

func warpedAutocorrelationSectionsNEON(st, corr *warpedAutocorrState, in []float32, w silkCReal, order int) int {
	pairs := order / 2
	warp := archsimd.BroadcastFloat64x2(w)
	negWarp := archsimd.BroadcastFloat64x2(-w)
	// Mask that selects lane 1 only ({false, true}).
	lane1Mask := archsimd.LoadInt64x2Array(&[2]int64{0, 1}).Greater(archsimd.BroadcastInt64x2(0))
	allMask := archsimd.LoadInt64x2Array(&[2]int64{1, 1}).Greater(archsimd.BroadcastInt64x2(0))

	n := 0
	for ; n+4 <= len(in); n += 4 {
		in4 := archsimd.LoadFloat32x4Array((*[4]float32)(in[n : n+4]))
		x0 := in4.ConvertLo2ToFloat64()
		inHi := in4.ToBits().ReshapeToUint64s()
		x1 := inHi.InterleaveHi(inHi).ReshapeToUint32s().BitsToFloat32().ConvertLo2ToFloat64()

		var t1Lo, prevT1Lo, t2Lo, ceLo, coLo archsimd.Float64x2
		var t1Hi, prevT1Hi, t2Hi, ceHi, coHi archsimd.Float64x2
		for q := 0; q <= pairs+3; q++ {
			switch q {
			case 0:
				t1Lo = x0.IfElse(allMask, t1Lo)
			case 1:
				t1Lo = x0.IfElse(lane1Mask, t1Lo)
			case 2:
				t1Hi = x1.IfElse(allMask, t1Hi)
			case 3:
				t1Hi = x1.IfElse(lane1Mask, t1Hi)
			}

			aLo := warpedShiftIn2(prevT1Lo, st, 2*q)
			bLo := warpedShiftIn2(t2Lo, st, 2*q+1)
			cLo := warpedShiftIn2(t1Lo, st, 2*q+2)
			aHi := warpedShiftAcross2(prevT1Lo, prevT1Hi)
			bHi := warpedShiftAcross2(t2Lo, t2Hi)
			cHi := warpedShiftAcross2(t1Lo, t1Hi)

			// Match arm64 scalar: st[i] + w*st[i+1] - w*tmp1 compiles to
			// FMADD(w, st[i+1], st[i]) followed by FMSUB(w, tmp1, ...).
			t2nLo := negWarp.MulAdd(t1Lo, warp.MulAdd(bLo, aLo))
			t2nHi := negWarp.MulAdd(t1Hi, warp.MulAdd(bHi, aHi))
			t1nLo := negWarp.MulAdd(t2nLo, warp.MulAdd(cLo, bLo))
			t1nHi := negWarp.MulAdd(t2nHi, warp.MulAdd(cHi, bHi))

			ceHi = x1.MulAdd(t1Hi, warpedShiftAcross2(ceLo, ceHi))
			coHi = x1.MulAdd(t2nHi, warpedShiftAcross2(coLo, coHi))
			ceLo = x0.MulAdd(t1Lo, warpedShiftIn2(ceLo, corr, 2*q))
			coLo = x0.MulAdd(t2nLo, warpedShiftIn2(coLo, corr, 2*q+1))

			if p := q - 3; p >= 0 && p <= pairs {
				warpedLane1Pair(t1Hi, t2nHi).StoreArray((*[2]silkCReal)(st[1+2*p : 3+2*p]))
				warpedLane1Pair(ceHi, coHi).StoreArray((*[2]silkCReal)(corr[1+2*p : 3+2*p]))
			}
			prevT1Lo, t1Lo, t2Lo = t1Lo, t1nLo, t2nLo
			prevT1Hi, t1Hi, t2Hi = t1Hi, t1nHi, t2nHi
		}
	}
	return n
}

// warpedShiftIn2 returns {v[1+i], x[0]}.
func warpedShiftIn2(x archsimd.Float64x2, v *warpedAutocorrState, i int) archsimd.Float64x2 {
	row := archsimd.LoadFloat64x2Array((*[2]silkCReal)(v[1+i : 3+i]))
	return row.ToBits().InterleaveLo(x.ToBits()).BitsToFloat64()
}

// warpedShiftAcross2 returns {lo[1], hi[0]}.
func warpedShiftAcross2(lo, hi archsimd.Float64x2) archsimd.Float64x2 {
	return hi.ToBits().ReshapeToUint8s().ConcatShiftBytesRight(lo.ToBits().ReshapeToUint8s(), 8).ReshapeToUint64s().BitsToFloat64()
}

// warpedLane1Pair returns {a[1], b[1]}.
func warpedLane1Pair(a, b archsimd.Float64x2) archsimd.Float64x2 {
	return a.ToBits().InterleaveHi(b.ToBits()).BitsToFloat64()
}
