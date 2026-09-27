//go:build amd64 && goexperiment.simd && !nosimd

package silk

import (
	"simd/archsimd"
	"unsafe"
)

// warpedAutocorrelationSections runs silk_warped_autocorrelation_FLP four
// input samples at a time as a wavefront: lane j of each Float64x4 carries
// sample n+j through allpass pair p = q-j at step q. A pair of sample n+j
// needs only sample n+j-1's results for the same pair and the pair after it,
// which lane j-1 produced one step earlier, so the four serial chains overlap
// while every state and correlation value sees the same double operations in
// the same order as the one-sample loop. The correlation accumulators travel
// with the lanes too, so each C[i] still sums the samples in input order.
func warpedAutocorrelationSections(st, corr *warpedAutocorrState, in []float32, w silkCReal, order int) {
	pairs := order / 2
	warp := archsimd.BroadcastFloat64x4(w)
	// startMask[q] selects the lanes at and above q: at step q < 4 lane q
	// starts its sample, and the lanes above it have not started yet.
	var startMask [4]archsimd.Mask64x4
	lanes := archsimd.LoadInt64x4Array(&[4]int64{0, 1, 2, 3})
	for q := range startMask {
		startMask[q] = lanes.Greater(archsimd.BroadcastInt64x4(int64(q) - 1))
	}
	// Row i of a state array starts at the element before C double i, so
	// its second element is C double i.
	st1 := unsafe.Pointer(&st[0])
	corr1 := unsafe.Pointer(&corr[0])
	n := 0
	for ; n+4 <= len(in); n += 4 {
		// The inputs are also the state[0] factors of each lane's
		// correlation terms.
		x := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Pointer(&in[n]))).ConvertToFloat64()
		// t1 is each lane's tmp1 entering its pair; prevT1 and t2 are the
		// previous step's tmp1 input and tmp2 result.
		var t1, prevT1, t2, ce, co archsimd.Float64x4
		for q := 0; q <= pairs+3; q++ {
			if q < 4 {
				t1 = x.IfElse(startMask[q], t1)
			}
			// Lane 0 reads the state and correlations sample n-1 left in
			// memory; lane j>0 takes the values lane j-1 produced for its
			// sample.
			a := warpedShiftIn(prevT1, st1, 2*q)
			b := warpedShiftIn(t2, st1, 2*q+1)
			c := warpedShiftIn(t1, st1, 2*q+2)
			// tmp2 = state[i] + warping*state[i+1] - warping*tmp1
			t2n := a.Add(warp.Mul(b)).Sub(warp.Mul(t1))
			// tmp1 = state[i+1] + warping*state[i+2] - warping*tmp2
			t1n := b.Add(warp.Mul(c)).Sub(warp.Mul(t2n))
			// C[i] += state[0]*tmp1; C[i+1] += state[0]*tmp2
			ce = warpedShiftIn(ce, corr1, 2*q).Add(x.Mul(t1))
			co = warpedShiftIn(co, corr1, 2*q+1).Add(x.Mul(t2n))

			// Lane 3 holds the last sample of the group: its pair p = q-3
			// leaves the final state and correlations for the next group.
			if p := q - 3; p >= 0 {
				if p < pairs {
					warpedLane3Pair(t1, t2n).StoreArray((*[2]silkCReal)(st[1+2*p : 3+2*p]))
					warpedLane3Pair(ce, co).StoreArray((*[2]silkCReal)(corr[1+2*p : 3+2*p]))
				} else {
					st[1+order] = t1.GetHi().GetElem(1)
					corr[1+order] = ce.GetHi().GetElem(1)
				}
			}
			prevT1, t1, t2 = t1, t1n, t2n
		}
	}
	// The wavefront leaves the upper register halves dirty; clear them
	// before the scalar SSE loop.
	archsimd.ClearAVXUpperBits()
	warpedAutocorrelationSamples(st, corr, in[n:], w, order)
}

// warpedShiftIn returns {c[i], x[0], x[1], x[2]}: every lane takes the value
// of the lane below it, and lane 0 takes C double i of the warpedAutocorrState
// that starts at v.
func warpedShiftIn(x archsimd.Float64x4, v unsafe.Pointer, i int) archsimd.Float64x4 {
	row := archsimd.LoadFloat64x4Array((*[4]silkCReal)(unsafe.Add(v, 8*i))) // {c[i-1], c[i], ...}
	u := row.ConcatPermute128Scalars(0, 2, x)                               // {c[i-1], c[i], x0, x1}
	return u.ConcatPermuteScalarsGrouped(1, 2, x)                           // {c[i], x0, x1, x2}
}

// warpedLane3Pair returns {a[3], b[3]}.
func warpedLane3Pair(a, b archsimd.Float64x4) archsimd.Float64x2 {
	return a.ConcatPermuteScalarsGrouped(1, 3, b).GetHi()
}
