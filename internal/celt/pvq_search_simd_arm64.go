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
	x = x[:n:n]
	absX = absX[:n:n]
	y = y[:n:n]
	signx = signx[:n:n]
	iy = iy[:n:n]
	var zero archsimd.Float32x4
	var zeroInt archsimd.Int32x4
	j := 0
	for ; j+4 <= n; j += 4 {
		v := archsimd.LoadFloat32x4Array((*[4]float32)(x[j : j+4]))
		zero.StoreArray((*[4]float32)(y[j : j+4]))
		zeroInt.StoreArray((*[4]int32)(iy[j : j+4]))
		v.Abs().StoreArray((*[4]float32)(absX[j : j+4]))
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

// pvqExtractAbsSignOnlySum extracts |x|, sign bits, and returns the exact
// ascending sum of |x| four NEON lanes at a time.
func pvqExtractAbsSignOnlySum(x []celtNorm, absX []float32, signx []byte, n int) float32 {
	if n < 4 {
		return pvqExtractAbsSignOnlySumScalar(x, absX, signx, n)
	}
	x = x[:n:n]
	absX = absX[:n:n]
	signx = signx[:n:n]
	var zero archsimd.Float32x4
	var sum float32
	j := 0
	for ; j+4 <= n; j += 4 {
		v := archsimd.LoadFloat32x4Array((*[4]float32)(x[j : j+4]))
		ax := v.Abs()
		ax.StoreArray((*[4]float32)(absX[j : j+4]))
		neg := v.Less(zero).ToInt32x4()
		s := signx[j : j+4 : j+4]
		s[0] = byte(neg.GetElem(0) & 1)
		s[1] = byte(neg.GetElem(1) & 1)
		s[2] = byte(neg.GetElem(2) & 1)
		s[3] = byte(neg.GetElem(3) & 1)
		sum = (((sum + ax.GetElem(0)) + ax.GetElem(1)) + ax.GetElem(2)) + ax.GetElem(3)
	}
	for ; j < n; j++ {
		signx[j] = 0
		xj := x[j]
		var ax float32
		if xj < 0 {
			signx[j] = 1
			ax = -xj
		} else {
			ax = xj
		}
		absX[j] = ax
		sum += ax
	}
	return sum
}

// pvqProjectHighPulse performs the high-pulse pyramid projection four NEON
// lanes at a time, rounding vector products and adding them in exact ascending
// element order, then finishing any n%4 tail with scalar FMA. This matches the
// selected ARM64 NEON libopus vq.c projection bit-for-bit.
func pvqProjectHighPulse(absX, y []float32, iy []int32, rcp float32, n, pulsesLeft int) (xy, yy float32, remPulses int) {
	if n < 4 {
		return pvqProjectHighPulseScalar(absX, y, iy, rcp, n, pulsesLeft)
	}
	absX = absX[:n:n]
	y = y[:n:n]
	iy = iy[:n:n]
	rcp4 := archsimd.BroadcastFloat32x4(rcp)
	var acc archsimd.Float32x4
	var pulseSub archsimd.Int32x4
	var zeroU archsimd.Uint64x2
	j := 0
	for ; j+4 <= n; j += 4 {
		ax := archsimd.LoadFloat32x4Array((*[4]float32)(absX[j : j+4]))
		iy4 := rcp4.Mul(ax).ConvertToInt32()
		iy4.StoreArray((*[4]int32)(iy[j : j+4]))
		y4 := iy4.ConvertToFloat32()
		y4.Add(y4).StoreArray((*[4]float32)(y[j : j+4]))
		pulseSub = pulseSub.Add(iy4)

		bYY := y4.Mul(y4).ToBits()
		bXY := ax.Mul(y4).ToBits()
		lo := bYY.InterleaveLo(bXY).ReshapeToUint64s()
		hi := bYY.InterleaveHi(bXY).ReshapeToUint64s()
		s0 := lo.InterleaveLo(zeroU).ReshapeToUint32s().BitsToFloat32()
		s1 := lo.InterleaveHi(zeroU).ReshapeToUint32s().BitsToFloat32()
		s2 := hi.InterleaveLo(zeroU).ReshapeToUint32s().BitsToFloat32()
		s3 := hi.InterleaveHi(zeroU).ReshapeToUint32s().BitsToFloat32()
		acc = acc.Add(s0).Add(s1).Add(s2).Add(s3)
	}
	yy = acc.GetElem(0)
	xy = acc.GetElem(1)
	remPulses = pulsesLeft - int(pulseSub.GetElem(0)+pulseSub.GetElem(1)+pulseSub.GetElem(2)+pulseSub.GetElem(3))
	for ; j < n; j++ {
		iyj := int32(rcp * absX[j])
		iy[j] = iyj
		yj := float32(iyj)
		yy += yj * yj
		xy += absX[j] * yj
		y[j] = yj * 2
		remPulses -= int(iyj)
	}
	return xy, yy, remPulses
}

// pvqRestoreSigns restores original signs to iy using 16-wide ARM64 NEON
// vector XOR-sub when n >= 16.
func pvqRestoreSigns(iy []int32, signx []byte, n int) {
	if n < 16 {
		pvqRestoreSignsScalar(iy, signx, n)
		return
	}
	iy = iy[:n:n]
	signx = signx[:n:n]
	var zero archsimd.Int32x4
	var zeroU8 archsimd.Uint8x16
	var zeroU16 archsimd.Uint16x8
	j := 0
	for ; j+16 <= n; j += 16 {
		sb := archsimd.LoadUint8x16Array((*[16]uint8)(signx[j : j+16]))
		s16Lo := sb.InterleaveLo(zeroU8).ReshapeToUint16s()
		s16Hi := sb.InterleaveHi(zeroU8).ReshapeToUint16s()

		m0 := zero.Sub(s16Lo.InterleaveLo(zeroU16).ReshapeToUint32s().BitsToInt32())
		v0 := archsimd.LoadInt32x4Array((*[4]int32)(iy[j : j+4]))
		v0.Xor(m0).Sub(m0).StoreArray((*[4]int32)(iy[j : j+4]))

		m1 := zero.Sub(s16Lo.InterleaveHi(zeroU16).ReshapeToUint32s().BitsToInt32())
		v1 := archsimd.LoadInt32x4Array((*[4]int32)(iy[j+4 : j+8]))
		v1.Xor(m1).Sub(m1).StoreArray((*[4]int32)(iy[j+4 : j+8]))

		m2 := zero.Sub(s16Hi.InterleaveLo(zeroU16).ReshapeToUint32s().BitsToInt32())
		v2 := archsimd.LoadInt32x4Array((*[4]int32)(iy[j+8 : j+12]))
		v2.Xor(m2).Sub(m2).StoreArray((*[4]int32)(iy[j+8 : j+12]))

		m3 := zero.Sub(s16Hi.InterleaveHi(zeroU16).ReshapeToUint32s().BitsToInt32())
		v3 := archsimd.LoadInt32x4Array((*[4]int32)(iy[j+12 : j+16]))
		v3.Xor(m3).Sub(m3).StoreArray((*[4]int32)(iy[j+12 : j+16]))
	}
	for ; j < n; j++ {
		mask := -int32(signx[j])
		iy[j] = (iy[j] ^ mask) - mask
	}
}

// pvqSearchPulseLoop places pulsesLeft pulses using the exact rate-distortion
// criterion bestDen*num > ryy*bestNum. On ARM64 NEON, eight candidates are
// evaluated per vector step with a single combined mask check; when no lane
// improves the current best (the common case across long bands), the block is
// skipped without scalar work. When any lane improves, the matching positions
// are resolved in exact ascending scalar order so ties and intermediate best
// updates are 100% bit-exact.
func pvqSearchPulseLoop(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32) {
	if n <= 0 || pulsesLeft <= 0 {
		return xy, yy
	}
	absX = absX[:n:n]
	y = y[:n:n]
	iy = iy[:n:n]
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
			if n >= 24 {
				for ; j+8 <= n; j += 8 {
					rxy0 := xy4.Add(archsimd.LoadFloat32x4Array((*[4]float32)(absX[j : j+4])))
					ryy0 := yy4.Add(archsimd.LoadFloat32x4Array((*[4]float32)(y[j : j+4])))
					num0 := rxy0.Mul(rxy0)
					rxy1 := xy4.Add(archsimd.LoadFloat32x4Array((*[4]float32)(absX[j+4 : j+8])))
					ryy1 := yy4.Add(archsimd.LoadFloat32x4Array((*[4]float32)(y[j+4 : j+8])))
					num1 := rxy1.Mul(rxy1)

					m0 := bestDen4.Mul(num0).Greater(ryy0.Mul(bestNum4)).ToInt32x4()
					m1 := bestDen4.Mul(num1).Greater(ryy1.Mul(bestNum4)).ToInt32x4()
					mAll := m0.Or(m1).ToBits().ReshapeToUint64s()
					if mAll.GetElem(0)|mAll.GetElem(1) != 0 {
						m0u := m0.ToBits().ReshapeToUint64s()
						if m0u.GetElem(0)|m0u.GetElem(1) != 0 {
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
							m1 = bestDen4.Mul(num1).Greater(ryy1.Mul(bestNum4)).ToInt32x4()
						}
						m1u := m1.ToBits().ReshapeToUint64s()
						if m1u.GetElem(0)|m1u.GetElem(1) != 0 {
							for k := range 4 {
								idx := j + 4 + k
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
			}
			for ; j+4 <= n; j += 4 {
				rxy4 := xy4.Add(archsimd.LoadFloat32x4Array((*[4]float32)(absX[j : j+4])))
				ryy4 := yy4.Add(archsimd.LoadFloat32x4Array((*[4]float32)(y[j : j+4])))
				num4 := rxy4.Mul(rxy4)
				m := bestDen4.Mul(num4).Greater(ryy4.Mul(bestNum4)).ToInt32x4().ToBits().ReshapeToUint64s()
				if m.GetElem(0)|m.GetElem(1) != 0 {
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
		for ; j+1 < n; j += 2 {
			rxy = xy + absX[j]
			ryy = yy + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}
			rxy = xy + absX[j+1]
			ryy = yy + y[j+1]
			num = rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j + 1
			}
		}
		if j < n {
			rxy = xy + absX[j]
			ryy = yy + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
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
