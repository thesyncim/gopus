//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// pvqExtractAbsSignNorm extracts |x|, sign bits, and zeroes y/iy four NEON
// lanes at a time, matching pvqExtractAbsSignNormScalar bit-for-bit.
func pvqExtractAbsSignNorm(x []celtNorm, absX []float32, y []float32, signx []byte, iy []int32, n int) {
	if n < 4 {
		pvqExtractAbsSignNormScalar(x, absX, y, signx, iy, n)
		return
	}
	x = x[:n]
	absX = absX[:n]
	y = y[:n]
	signx = signx[:n]
	iy = iy[:n]
	var zero archsimd.Float32x4
	var zeroInt archsimd.Int32x4
	j := 0
	for ; j+4 <= n; j += 4 {
		v := archsimd.LoadFloat32x4Array((*[4]float32)(x[j:]))
		zero.StoreArray((*[4]float32)(y[j:]))
		zeroInt.StoreArray((*[4]int32)(iy[j:]))
		v.Abs().StoreArray((*[4]float32)(absX[j:]))
		neg := v.Less(zero).ToInt32x4()
		s := signx[j : j+4 : j+4]
		s[0] = byte(neg.GetElem(0) & 1)
		s[1] = byte(neg.GetElem(1) & 1)
		s[2] = byte(neg.GetElem(2) & 1)
		s[3] = byte(neg.GetElem(3) & 1)
	}
	for ; j < n; j++ {
		iy[j] = 0
		signx[j] = 0
		y[j] = 0
		xj := x[j]
		if xj < 0 {
			signx[j] = 1
			absX[j] = -xj
		} else {
			absX[j] = xj
		}
	}
}

// pvqSearchPulseLoop places pulsesLeft pulses using the exact rate-distortion
// criterion bestDen*num > ryy*bestNum. On ARM64 NEON, four candidates are
// evaluated per vector step; when no lane improves the current best (the
// common case across long bands), the block is skipped without scalar work.
// When any lane improves, the four positions are resolved in exact ascending
// scalar order so ties and intermediate best updates are 100% bit-exact.
func pvqSearchPulseLoop(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32) {
	if n <= 0 || pulsesLeft <= 0 {
		return xy, yy
	}
	absX = absX[:n]
	y = y[:n]
	iy = iy[:n]
	for range pulsesLeft {
		yy += 1

		bestID := 0
		rxy := xy + absX[0]
		ryy := yy + y[0]
		bestNum := rxy * rxy
		bestDen := ryy

		j := 1
		if n >= 16 {
			xy4 := archsimd.BroadcastFloat32x4(xy)
			yy4 := archsimd.BroadcastFloat32x4(yy)
			bestNum4 := archsimd.BroadcastFloat32x4(bestNum)
			bestDen4 := archsimd.BroadcastFloat32x4(bestDen)
			for ; j+4 <= n; j += 4 {
				rxy4 := xy4.Add(archsimd.LoadFloat32x4Array((*[4]float32)(absX[j:])))
				ryy4 := yy4.Add(archsimd.LoadFloat32x4Array((*[4]float32)(y[j:])))
				num4 := rxy4.Mul(rxy4)
				m := bestDen4.Mul(num4).Greater(ryy4.Mul(bestNum4)).ToInt32x4()
				if (m.GetElem(0)|m.GetElem(1))|(m.GetElem(2)|m.GetElem(3)) != 0 {
					for k := range 4 {
						idx := j + k
						rk := xy + absX[idx]
						yk := yy + y[idx]
						nk := rk * rk
						if bestDen*nk > yk*bestNum {
							bestDen = yk
							bestNum = nk
							bestID = idx
						}
					}
					bestNum4 = archsimd.BroadcastFloat32x4(bestNum)
					bestDen4 = archsimd.BroadcastFloat32x4(bestDen)
				}
			}
		}
		for ; j < n; j++ {
			rxy = xy + absX[j]
			ryy = yy + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}
		}

		xy += absX[bestID]
		yy += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}
	return xy, yy
}
