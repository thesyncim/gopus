package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	spreadNone       = 0
	spreadLight      = 1
	spreadNormal     = 2
	spreadAggressive = 3
)

var orderyTable = []int{
	1, 0,
	3, 0, 2, 1,
	7, 0, 4, 3, 6, 1, 5, 2,
	15, 0, 8, 7, 12, 3, 11, 4, 14, 1, 9, 6, 13, 2, 10, 5,
}

var bitInterleaveTable = []int{
	0, 1, 1, 1,
	2, 3, 3, 3,
	2, 3, 3, 3,
	2, 3, 3, 3,
}

var bitDeinterleaveTable = []int{
	0x00, 0x03, 0x0C, 0x0F,
	0x30, 0x33, 0x3C, 0x3F,
	0xC0, 0xC3, 0xCC, 0xCF,
	0xF0, 0xF3, 0xFC, 0xFF,
}

type bandCtx struct {
	rd              *rangecoding.Decoder
	spread          int
	tfChange        int
	remainingBits   int
	intensity       int
	band            int
	seed            *uint32
	resynth         bool
	disableInv      bool
	avoidSplitNoise bool
}

type splitCtx struct {
	inv    int
	imid   int
	iside  int
	delta  int
	itheta int
	qalloc int
}

func orderyForStride(stride int) []int {
	switch stride {
	case 2:
		return orderyTable[0:2]
	case 4:
		return orderyTable[2:6]
	case 8:
		return orderyTable[6:14]
	case 16:
		return orderyTable[14:30]
	default:
		return nil
	}
}

func deinterleaveHadamard(x []float64, n0, stride int, hadamard bool) {
	n := n0 * stride
	tmp := make([]float64, n)
	if hadamard {
		ordery := orderyForStride(stride)
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[ordery[i]*n0+j] = x[j*stride+i]
			}
		}
	} else {
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[i*n0+j] = x[j*stride+i]
			}
		}
	}
	copy(x, tmp)
}

func interleaveHadamard(x []float64, n0, stride int, hadamard bool) {
	n := n0 * stride
	tmp := make([]float64, n)
	if hadamard {
		ordery := orderyForStride(stride)
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[j*stride+i] = x[ordery[i]*n0+j]
			}
		}
	} else {
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[j*stride+i] = x[i*n0+j]
			}
		}
	}
	copy(x, tmp)
}

func haar1(x []float64, n0, stride int) {
	n0 >>= 1
	invSqrt2 := 0.7071067811865476
	for i := 0; i < stride; i++ {
		for j := 0; j < n0; j++ {
			idx0 := stride*2*j + i
			idx1 := stride*(2*j+1) + i
			tmp1 := invSqrt2 * x[idx0]
			tmp2 := invSqrt2 * x[idx1]
			x[idx0] = tmp1 + tmp2
			x[idx1] = tmp1 - tmp2
		}
	}
}

func expRotation1(x []float64, length, stride int, c, s float64) {
	ms := -s
	for i := 0; i < length-stride; i++ {
		x1 := x[i]
		x2 := x[i+stride]
		x[i+stride] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2
	}
	for i := length - 2*stride - 1; i >= 0; i-- {
		x1 := x[i]
		x2 := x[i+stride]
		x[i+stride] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2
	}
}

func expRotation(x []float64, length, dir, stride, k, spread int) {
	if 2*k >= length || spread == spreadNone {
		return
	}
	spreadFactor := []int{15, 10, 5}[spread-1]
	gain := float64(length) / float64(length+spreadFactor*k)
	theta := 0.5 * gain * gain
	c := math.Cos(0.5 * math.Pi * theta)
	s := math.Sin(0.5 * math.Pi * theta)

	stride2 := 0
	if length >= 8*stride {
		stride2 = 1
		for (stride2*stride2+stride2)*stride+(stride>>2) < length {
			stride2++
		}
	}
	length = celtUdiv(length, stride)
	for i := 0; i < stride; i++ {
		off := i * length
		if dir < 0 {
			if stride2 != 0 {
				expRotation1(x[off:], length, stride2, s, c)
			}
			expRotation1(x[off:], length, 1, c, s)
		} else {
			expRotation1(x[off:], length, 1, c, -s)
			if stride2 != 0 {
				expRotation1(x[off:], length, stride2, s, -c)
			}
		}
	}
}

func extractCollapseMask(pulses []int, n, b int) int {
	if b <= 1 {
		return 1
	}
	n0 := celtUdiv(n, b)
	mask := 0
	for i := 0; i < b; i++ {
		tmp := 0
		for j := 0; j < n0; j++ {
			tmp |= pulses[i*n0+j]
		}
		if tmp != 0 {
			mask |= 1 << i
		}
	}
	return mask
}

func normalizeResidual(pulses []int, gain float64) []float64 {
	out := make([]float64, len(pulses))
	var energy float64
	for _, v := range pulses {
		energy += float64(v * v)
	}
	if energy <= 0 {
		return out
	}
	scale := gain / math.Sqrt(energy)
	for i, v := range pulses {
		out[i] = float64(v) * scale
	}
	return out
}

func renormalizeVector(x []float64, gain float64) {
	if len(x) == 0 {
		return
	}
	energy := 0.0
	for _, v := range x {
		energy += v * v
	}
	if energy <= 0 {
		return
	}
	scale := gain / math.Sqrt(energy)
	for i := range x {
		x[i] *= scale
	}
}

func stereoMerge(x, y []float64, mid float64) {
	n := len(x)
	if n == 0 || len(y) < n {
		return
	}
	xp := 0.0
	side := 0.0
	for i := 0; i < n; i++ {
		xp += y[i] * x[i]
		side += y[i] * y[i]
	}
	xp *= mid
	el := mid*mid/8.0 + side - 2.0*xp
	er := mid*mid/8.0 + side + 2.0*xp
	if el < 6e-4 || er < 6e-4 {
		copy(y, x[:n])
		return
	}
	lgain := 1.0 / math.Sqrt(el)
	rgain := 1.0 / math.Sqrt(er)
	for i := 0; i < n; i++ {
		l := mid * x[i]
		r := y[i]
		x[i] = (l - r) * lgain
		y[i] = (l + r) * rgain
	}
}

func specialHybridFolding(norm, norm2 []float64, start, M int, dualStereo bool) {
	if start+2 >= len(EBands) {
		return
	}
	n1 := M * (EBands[start+1] - EBands[start])
	n2 := M * (EBands[start+2] - EBands[start+1])
	if n2 <= n1 {
		return
	}
	src := 2*n1 - n2
	dst := n1
	count := n2 - n1
	if src < 0 || src+count > len(norm) || dst+count > len(norm) {
		return
	}
	copy(norm[dst:dst+count], norm[src:src+count])
	if dualStereo && norm2 != nil {
		if src < 0 || src+count > len(norm2) || dst+count > len(norm2) {
			return
		}
		copy(norm2[dst:dst+count], norm2[src:src+count])
	}
}

func algUnquant(rd *rangecoding.Decoder, n, k, spread, b int, gain float64) ([]float64, int) {
	if k <= 0 || n <= 0 {
		return make([]float64, n), 0
	}
	if rd == nil {
		return make([]float64, n), 0
	}
	u := make([]uint32, k+2)
	vSize := ncwrsUrow(n, k, u)
	if vSize == 0 {
		return make([]float64, n), 0
	}
	idx := rd.DecodeUniform(vSize)
	pulses := make([]int, n)
	_ = cwrsi(n, k, idx, pulses, u)
	DefaultTracer.TracePVQ(-1, idx, k, n, pulses)
	shape := normalizeResidual(pulses, gain)
	expRotation(shape, n, -1, b, k, spread)
	cm := extractCollapseMask(pulses, n, b)
	return shape, cm
}

func computeQn(n, b, offset, pulseCap int, stereo bool) int {
	exp2Table := []int{16384, 17866, 19483, 21247, 23170, 25267, 27554, 30048}
	n2 := 2*n - 1
	if stereo && n == 2 {
		n2--
	}
	qb := celtSudiv(b+n2*offset, n2)
	qb = minInt(b-pulseCap-(4<<bitRes), qb)
	qb = minInt(8<<bitRes, qb)
	if qb < (1 << (bitRes - 1)) {
		return 1
	}
	qn := exp2Table[qb&0x7] >> (14 - (qb >> bitRes))
	qn = ((qn + 1) >> 1) << 1
	if qn > 256 {
		qn = 256
	}
	return qn
}

func computeTheta(ctx *bandCtx, sctx *splitCtx, n int, b *int, B, B0, lm int, stereo bool, fill *int) {
	pulseCap := LogN[ctx.band] + lm*(1<<bitRes)
	offset := (pulseCap >> 1) - qthetaOffset
	if stereo && n == 2 {
		offset = (pulseCap >> 1) - qthetaOffsetTwoPhase
	}
	qn := computeQn(n, *b, offset, pulseCap, stereo)
	if stereo && ctx.band >= ctx.intensity {
		qn = 1
	}

	tell := 0
	if ctx.rd != nil {
		tell = ctx.rd.TellFrac()
	}
	itheta := 0
	inv := 0
	if qn != 1 {
		if stereo && n > 2 {
			p0 := 3
			x0 := qn / 2
			ft := p0*(x0+1) + x0
			if ctx.rd != nil {
				fm := int(ctx.rd.Decode(uint32(ft)))
				if fm < (x0+1)*p0 {
					itheta = fm / p0
				} else {
					itheta = x0 + 1 + (fm - (x0+1)*p0)
				}
				var fl int
				if itheta <= x0 {
					fl = p0 * itheta
					ctx.rd.Update(uint32(fl), uint32(fl+p0), uint32(ft))
				} else {
					fl = (itheta - 1 - x0) + (x0+1)*p0
					ctx.rd.Update(uint32(fl), uint32(fl+1), uint32(ft))
				}
			}
		} else if B0 > 1 || stereo {
			if ctx.rd != nil {
				itheta = int(ctx.rd.DecodeUniform(uint32(qn + 1)))
			}
		} else {
			ft := ((qn >> 1) + 1) * ((qn >> 1) + 1)
			if ctx.rd != nil {
				fm := int(ctx.rd.Decode(uint32(ft)))
				if fm < ((qn >> 1) * ((qn >> 1) + 1) >> 1) {
					itheta = int((isqrt32(uint32(8*fm+1)) - 1) >> 1)
					fs := itheta + 1
					fl := itheta * (itheta + 1) >> 1
					ctx.rd.Update(uint32(fl), uint32(fl+fs), uint32(ft))
				} else {
					itheta = int((2*(qn+1) - int(isqrt32(uint32(8*(ft-fm-1)+1)))) >> 1)
					fs := qn + 1 - itheta
					fl := ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1)
					ctx.rd.Update(uint32(fl), uint32(fl+fs), uint32(ft))
				}
			}
		}
		itheta = celtUdiv(itheta*16384, qn)
	} else if stereo {
		if *b > 2<<bitRes && ctx.remainingBits > 2<<bitRes {
			if ctx.rd != nil {
				inv = ctx.rd.DecodeBit(2)
			}
		}
		if ctx.disableInv {
			inv = 0
		}
		itheta = 0
	}

	if ctx.rd != nil {
		qalloc := ctx.rd.TellFrac() - tell
		*b -= qalloc
		sctx.qalloc = qalloc
	}

	imid := 0
	iside := 0
	delta := 0
	if itheta == 0 {
		imid = 32767
		iside = 0
		*fill &= (1 << B) - 1
		delta = -16384
	} else if itheta == 16384 {
		imid = 0
		iside = 32767
		*fill &= ((1 << B) - 1) << B
		delta = 16384
	} else {
		imid = bitexactCos(itheta)
		iside = bitexactCos(16384 - itheta)
		delta = fracMul16((n-1)<<7, bitexactLog2tan(iside, imid))
	}

	sctx.inv = inv
	sctx.imid = imid
	sctx.iside = iside
	sctx.delta = delta
	sctx.itheta = itheta
}

func quantPartition(ctx *bandCtx, x []float64, n, b, B int, lowband []float64, lm int, gain float64, fill int) (int, []float64) {
	if n == 1 {
		return 1, x
	}

	maxBits := 0
	if lm != -1 {
		if cache, ok := pulseCacheForBand(ctx.band, lm); ok {
			maxBits = int(cache[int(cache[0])])
		}
	}

	if lm != -1 && b > maxBits+12 && n > 2 {
		nHalf := n >> 1
		y := x[nHalf:]
		lm--
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		sctx := splitCtx{}
		computeTheta(ctx, &sctx, nHalf, &b, B, B, lm, false, &fill)
		mid := float64(sctx.imid) / 32768.0
		side := float64(sctx.iside) / 32768.0
		if B > 1 && (sctx.itheta&0x3fff) != 0 {
			if sctx.itheta > 8192 {
				sctx.delta -= sctx.delta >> (4 - lm)
			} else {
				sctx.delta = minInt(0, sctx.delta+(nHalf<<bitRes>>(5-lm)))
			}
		}
		mbits := maxInt(0, minInt(b, (b-sctx.delta)/2))
		sbits := b - mbits
		ctx.remainingBits -= sctx.qalloc

		var lowband1 []float64
		var lowband2 []float64
		if lowband != nil && len(lowband) >= nHalf {
			lowband1 = lowband[:nHalf]
		}
		if lowband != nil && len(lowband) >= n {
			lowband2 = lowband[nHalf:]
		}

		rebalance := ctx.remainingBits
		var cm int
		if mbits >= sbits {
			cm, _ = quantPartition(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, gain*mid, fill)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			scm, _ := quantPartition(ctx, y, nHalf, sbits, B, lowband2, lm, gain*side, fill>>B)
			cm |= scm << (B >> 1)
		} else {
			cm, _ = quantPartition(ctx, y, nHalf, sbits, B, lowband2, lm, gain*side, fill>>B)
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			scm, _ := quantPartition(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, gain*mid, fill)
			cm |= scm
		}
		return cm, x
	}

	q := bitsToPulses(ctx.band, lm, b)
	currBits := pulsesToBits(ctx.band, lm, q)
	ctx.remainingBits -= currBits
	for ctx.remainingBits < 0 && q > 0 {
		ctx.remainingBits += currBits
		q--
		currBits = pulsesToBits(ctx.band, lm, q)
		ctx.remainingBits -= currBits
	}
	if q != 0 {
		k := getPulses(q)
		shape, cm := algUnquant(ctx.rd, n, k, ctx.spread, B, gain)
		copy(x, shape)
		return cm, x
	}

	if ctx.resynth {
		cmMask := (1 << B) - 1
		fill &= cmMask
		if fill == 0 {
			for i := range x {
				x[i] = 0
			}
			return 0, x
		}
		if lowband == nil {
			if ctx.seed != nil {
				for i := range x {
					*ctx.seed = (*ctx.seed)*1664525 + 1013904223
					x[i] = float64(int32(*ctx.seed>>20)) / 32768.0
				}
			}
			renormalizeVector(x, gain)
			return cmMask, x
		}
		if ctx.seed != nil {
			for i := range x {
				*ctx.seed = (*ctx.seed)*1664525 + 1013904223
				tmp := 1.0 / 256.0
				if (*ctx.seed & 0x8000) == 0 {
					tmp = -tmp
				}
				if i < len(lowband) {
					x[i] = lowband[i] + tmp
				} else {
					x[i] = tmp
				}
			}
		}
		renormalizeVector(x, gain)
		return fill, x
	}
	return fill, x
}

func quantBandN1(ctx *bandCtx, x, y []float64, lowbandOut []float64) int {
	stereo := y != nil
	x0 := x
	for c := 0; c < 1+boolToInt(stereo); c++ {
		sign := 0
		if ctx.remainingBits >= 1<<bitRes {
			if ctx.rd != nil {
				sign = int(ctx.rd.DecodeRawBits(1))
			}
			ctx.remainingBits -= 1 << bitRes
		}
		if ctx.resynth {
			val := 1.0
			if sign != 0 {
				val = -1.0
			}
			x[0] = val
		}
		if stereo {
			x = y
		}
	}
	if lowbandOut != nil && len(lowbandOut) > 0 {
		lowbandOut[0] = x0[0] / 16.0
	}
	return 1
}

func quantBand(ctx *bandCtx, x []float64, n, b, B int, lowband []float64, lm int, lowbandOut []float64, gain float64, lowbandScratch []float64, fill int) int {
	if n == 1 {
		return quantBandN1(ctx, x, nil, lowbandOut)
	}

	N0 := n
	N_B := celtUdiv(n, B)
	longBlocks := B == 1

	recombine := 0
	tfChange := ctx.tfChange
	if tfChange > 0 {
		recombine = tfChange
	}

	if lowbandScratch != nil && lowband != nil && (recombine != 0 || ((N_B&1) == 0 && tfChange < 0) || B > 1) {
		copy(lowbandScratch, lowband)
		lowband = lowbandScratch
	}

	if recombine != 0 {
		for k := 0; k < recombine; k++ {
			haar1(x, n>>k, 1<<k)
			if lowband != nil {
				haar1(lowband, n>>k, 1<<k)
			}
			fill = bitInterleaveTable[fill&0xF] | (bitInterleaveTable[fill>>4] << 2)
		}
	}
	B >>= recombine
	N_B <<= recombine

	timeDivide := 0
	for (N_B&1) == 0 && tfChange < 0 {
		haar1(x, N_B, B)
		if lowband != nil {
			haar1(lowband, N_B, B)
		}
		fill |= fill << B
		B <<= 1
		N_B >>= 1
		timeDivide++
		tfChange++
	}
	B0 := B
	N_B0 := N_B

	if B0 > 1 {
		deinterleaveHadamard(x, N_B>>recombine, B0<<recombine, longBlocks)
		if lowband != nil {
			deinterleaveHadamard(lowband, N_B>>recombine, B0<<recombine, longBlocks)
		}
	}

	cm, _ := quantPartition(ctx, x, n, b, B, lowband, lm, gain, fill)

	if ctx.resynth {
		if B0 > 1 {
			interleaveHadamard(x, N_B>>recombine, B0<<recombine, longBlocks)
		}
		N_B = N_B0
		B = B0
		for k := 0; k < timeDivide; k++ {
			B >>= 1
			N_B <<= 1
			cm |= cm >> B
			haar1(x, N_B, B)
		}
		for k := 0; k < recombine; k++ {
			cm = bitDeinterleaveTable[cm&0xF]
			haar1(x, N0>>k, 1<<k)
		}
		B <<= recombine

		if lowbandOut != nil && len(lowbandOut) >= N0 {
			norm := math.Sqrt(float64(N0))
			for j := 0; j < N0; j++ {
				lowbandOut[j] = norm * x[j]
			}
		}
		cm &= (1 << B) - 1
	}
	return cm
}

func quantBandStereo(ctx *bandCtx, x, y []float64, n, b, B int, lowband []float64, lm int, lowbandOut []float64, lowbandScratch []float64, fill int) int {
	if n == 1 {
		return quantBandN1(ctx, x, y, lowbandOut)
	}

	origFill := fill
	sctx := splitCtx{}
	computeTheta(ctx, &sctx, n, &b, B, B, lm, true, &fill)
	mid := float64(sctx.imid) / 32768.0
	side := float64(sctx.iside) / 32768.0

	if n == 2 {
		mbits := b
		sbits := 0
		if sctx.itheta != 0 && sctx.itheta != 16384 {
			sbits = 1 << bitRes
		}
		mbits -= sbits
		c := sctx.itheta > 8192
		ctx.remainingBits -= sctx.qalloc + sbits

		x2 := x
		y2 := y
		if c {
			x2 = y
			y2 = x
		}
		sign := 1
		if sbits > 0 && ctx.rd != nil {
			if ctx.rd.DecodeRawBits(1) == 1 {
				sign = -1
			}
		}
		cm := quantBand(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, origFill)
		y2[0] = float64(sign) * (-x2[1])
		y2[1] = float64(sign) * x2[0]
		if ctx.resynth {
			x[0] *= mid
			x[1] *= mid
			y[0] *= side
			y[1] *= side
			tmp := x[0]
			x[0] = tmp - y[0]
			y[0] = tmp + y[0]
			tmp = x[1]
			x[1] = tmp - y[1]
			y[1] = tmp + y[1]
		}
		return cm
	}

	delta := sctx.delta
	mbits := maxInt(0, minInt(b, (b-delta)/2))
	sbits := b - mbits
	ctx.remainingBits -= sctx.qalloc

	rebalance := ctx.remainingBits
	cm := 0
	if mbits >= sbits {
		cm = quantBand(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill)
		rebalance = mbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 0 {
			sbits += rebalance - (3 << bitRes)
		}
		cm |= quantBand(ctx, y, n, sbits, B, nil, lm, nil, side, nil, fill>>B)
	} else {
		cm = quantBand(ctx, y, n, sbits, B, nil, lm, nil, side, nil, fill>>B)
		rebalance = sbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 16384 {
			mbits += rebalance - (3 << bitRes)
		}
		cm |= quantBand(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill)
	}

	if ctx.resynth {
		if n != 2 {
			stereoMerge(x, y, mid)
		}
		if sctx.inv != 0 {
			for i := 0; i < n; i++ {
				y[i] = -y[i]
			}
		}
	}
	return cm
}

func quantAllBandsDecode(rd *rangecoding.Decoder, channels, frameSize, lm int, start, end int,
	pulses []int, shortBlocks int, spread int, dualStereo, intensity int,
	tfRes []int, totalBitsQ3 int, balance int, codedBands int, seed *uint32) (left, right []float64, collapse []byte) {
	M := 1 << lm
	B := 1
	if shortBlocks > 1 {
		B = shortBlocks
	}
	N := frameSize
	left = make([]float64, N)
	if channels == 2 {
		right = make([]float64, N)
	}
	collapse = make([]byte, channels*MaxBands)

	normOffset := M * EBands[start]
	normLen := M*EBands[MaxBands-1] - normOffset
	if normLen < 0 {
		normLen = 0
	}
	norm := make([]float64, channels*normLen)
	var norm2 []float64
	if channels == 2 {
		norm2 = norm[normLen:]
	}

	maxBand := M * (EBands[end] - EBands[end-1])
	lowbandScratch := make([]float64, maxBand)

	lowbandOffset := 0
	updateLowband := true
	ctx := bandCtx{
		rd:              rd,
		spread:          spread,
		remainingBits:   0,
		intensity:       intensity,
		seed:            seed,
		resynth:         true,
		disableInv:      false,
		avoidSplitNoise: B > 1,
	}

	for i := start; i < end; i++ {
		ctx.band = i
		last := i == end-1
		bandStart := EBands[i] * M
		bandEnd := EBands[i+1] * M
		nBand := bandEnd - bandStart
		if nBand <= 0 {
			continue
		}

		x := left[bandStart:bandEnd]
		var y []float64
		if channels == 2 {
			y = right[bandStart:bandEnd]
		}

		tell := rd.TellFrac()
		if i != start {
			balance -= tell
		}
		remaining := totalBitsQ3 - tell - 1
		ctx.remainingBits = remaining

		b := 0
		if i <= codedBands-1 {
			currBalance := celtSudiv(balance, minInt(3, codedBands-i))
			b = maxInt(0, minInt(16383, minInt(remaining+1, pulses[i]+currBalance)))
		}

		if ctx.resynth && (M*EBands[i]-nBand >= M*EBands[start] || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		if i == start+1 {
			specialHybridFolding(norm, norm2, start, M, dualStereo != 0)
		}

		ctx.tfChange = tfRes[i]
		if dualStereo != 0 && i == intensity {
			dualStereo = 0
			if ctx.resynth {
				for j := 0; j < len(norm2) && j < len(norm); j++ {
					norm[j] = 0.5 * (norm[j] + norm2[j])
				}
			}
		}

		effectiveLowband := -1
		xCM := 0
		yCM := 0
		if lowbandOffset != 0 && (spread != spreadAggressive || B > 1 || ctx.tfChange < 0) {
			effectiveLowband = maxInt(0, M*EBands[lowbandOffset]-normOffset-nBand)
			foldStart := lowbandOffset
			for foldStart > start && M*EBands[foldStart-1] > effectiveLowband+normOffset {
				foldStart--
			}
			foldEnd := lowbandOffset - 1
			for foldEnd+1 < i && M*EBands[foldEnd+1] < effectiveLowband+normOffset+nBand {
				foldEnd++
			}
			for fold := foldStart; fold <= foldEnd; fold++ {
				xCM |= int(collapse[fold*channels])
				if channels == 2 {
					yCM |= int(collapse[fold*channels+channels-1])
				}
			}
		} else {
			xCM = (1 << B) - 1
			yCM = xCM
		}

		var lowbandX []float64
		var lowbandY []float64
		if effectiveLowband >= 0 && effectiveLowband+nBand <= len(norm) {
			lowbandX = norm[effectiveLowband : effectiveLowband+nBand]
			if channels == 2 && effectiveLowband+nBand <= len(norm2) {
				lowbandY = norm2[effectiveLowband : effectiveLowband+nBand]
			}
		}

		var lowbandOutX []float64
		var lowbandOutY []float64
		outStart := M*EBands[i] - normOffset
		if !last && outStart >= 0 && outStart+nBand <= len(norm) {
			lowbandOutX = norm[outStart : outStart+nBand]
			if channels == 2 && outStart+nBand <= len(norm2) {
				lowbandOutY = norm2[outStart : outStart+nBand]
			}
		}

		if dualStereo != 0 {
			xCM = quantBand(&ctx, x, nBand, b/2, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM)
			if channels == 2 {
				yCM = quantBand(&ctx, y, nBand, b/2, B, lowbandY, lm, lowbandOutY, 1.0, lowbandScratch, yCM)
			}
		} else {
			if channels == 2 {
				xCM = quantBandStereo(&ctx, x, y, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, xCM|yCM)
				yCM = xCM
			} else {
				xCM = quantBand(&ctx, x, nBand, b, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM|yCM)
				yCM = xCM
			}
		}

		collapse[i*channels] = byte(xCM)
		if channels == 2 {
			collapse[i*channels+channels-1] = byte(yCM)
		}
		balance += pulses[i] + tell

		updateLowband = b > (nBand << bitRes)
		ctx.avoidSplitNoise = false
	}

	return left, right, collapse
}
