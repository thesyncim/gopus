package celt

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func extractCollapseMask(pulses []int32, n, b int) int {
	if b <= 1 || n <= 0 {
		return 1
	}
	n0 := celtUdivBlocks(n, b)
	if n0 <= 0 {
		return 0
	}
	pulses = pulses[:n0*b]
	mask := 0
	for i := range b {
		var tmp int32
		for _, p := range pulses[i*n0 : (i+1)*n0] {
			tmp |= p
		}
		// tmp|-tmp has its sign bit set exactly when tmp != 0.
		mask |= int(uint32(tmp|-tmp)>>31) << i
	}
	return mask
}

// normalizeResidualInto normalizes the pulse vector into a pre-allocated output buffer.
func normalizeResidualInto(out []celtNorm, pulses []int, gain opusVal16, yy opusVal16) {
	n := len(pulses)
	if len(out) < n {
		return
	}
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy := float32(yy)
	if energy <= 0 {
		// Fall back to computing energy from pulses.
		i := 0
		for ; i+3 < n; i += 4 {
			v0 := float32(pulses[i])
			v1 := float32(pulses[i+1])
			v2 := float32(pulses[i+2])
			v3 := float32(pulses[i+3])
			energy += v0*v0 + v1*v1 + v2*v2 + v3*v3
		}
		for ; i < n; i++ {
			v := float32(pulses[i])
			energy += v * v
		}
	}
	if energy <= 0 {
		clear(out[:n])
		return
	}
	scale := celtRSqrt(energy) * float32(gain)
	i := 0
	for ; i+3 < n; i += 4 {
		out[i] = celtNorm(float32(pulses[i]) * scale)
		out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
		out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
		out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
	}
	for ; i < n; i++ {
		out[i] = celtNorm(float32(pulses[i]) * scale)
	}
}

func normalizeResidualInto32(out []celtNorm, pulses []int32, gain opusVal16, yy opusVal16) {
	n := len(pulses)
	if len(out) < n {
		return
	}
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy := float32(yy)
	if energy <= 0 {
		i := 0
		for ; i+3 < n; i += 4 {
			v0 := float32(pulses[i])
			v1 := float32(pulses[i+1])
			v2 := float32(pulses[i+2])
			v3 := float32(pulses[i+3])
			energy += v0*v0 + v1*v1 + v2*v2 + v3*v3
		}
		for ; i < n; i++ {
			v := float32(pulses[i])
			energy += v * v
		}
	}
	if energy <= 0 {
		clear(out[:n])
		return
	}
	scale := celtRSqrt(energy) * float32(gain)
	i := 0
	for ; i+3 < n; i += 4 {
		out[i] = celtNorm(float32(pulses[i]) * scale)
		out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
		out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
		out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
	}
	for ; i < n; i++ {
		out[i] = celtNorm(float32(pulses[i]) * scale)
	}
}

// normalizeResidualIntoAndCollapse normalizes the pulse vector into out and
// computes the collapse mask in the same pass.
func normalizeResidualIntoAndCollapse(out []celtNorm, pulses []int, gain opusVal16, yy opusVal16, b int) int {
	n := len(pulses)
	if len(out) < n {
		return 0
	}
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy := float32(yy)
	if energy <= 0 {
		// Fall back to computing energy from pulses.
		i := 0
		for ; i+3 < n; i += 4 {
			v0 := float32(pulses[i])
			v1 := float32(pulses[i+1])
			v2 := float32(pulses[i+2])
			v3 := float32(pulses[i+3])
			energy += v0*v0 + v1*v1 + v2*v2 + v3*v3
		}
		for ; i < n; i++ {
			v := float32(pulses[i])
			energy += v * v
		}
	}
	if energy <= 0 {
		clear(out[:n])
		if b <= 1 {
			return 1
		}
		return 0
	}
	return normalizeResidualKnownEnergyIntoAndCollapse(out, pulses, gain, opusVal16(energy), b)
}

func normalizeResidualIntoAndCollapse32(out []celtNorm, pulses []int32, gain opusVal16, yy opusVal16, b int) int {
	n := len(pulses)
	if len(out) < n {
		return 0
	}
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy := float32(yy)
	if energy <= 0 {
		i := 0
		for ; i+3 < n; i += 4 {
			v0 := float32(pulses[i])
			v1 := float32(pulses[i+1])
			v2 := float32(pulses[i+2])
			v3 := float32(pulses[i+3])
			energy += v0*v0 + v1*v1 + v2*v2 + v3*v3
		}
		for ; i < n; i++ {
			v := float32(pulses[i])
			energy += v * v
		}
	}
	if energy <= 0 {
		clear(out[:n])
		if b <= 1 {
			return 1
		}
		return 0
	}
	return normalizeResidualKnownEnergyIntoAndCollapse32(out, pulses, gain, opusVal16(energy), b)
}

func normalizeResidualKnownEnergyIntoAndCollapse(out []celtNorm, pulses []int, gain opusVal16, energy opusVal16, b int) int {
	n := len(pulses)
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy32 := float32(energy)
	if energy32 <= 0 {
		for i := range n {
			v := float32(pulses[i])
			energy32 += v * v
		}
	}
	if energy32 <= 0 {
		clear(out[:n])
		if b <= 1 {
			return 1
		}
		return 0
	}
	scale := celtRSqrt(energy32) * float32(gain)

	if b <= 1 {
		i := 0
		for ; i+3 < n; i += 4 {
			out[i] = celtNorm(float32(pulses[i]) * scale)
			out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
			out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
			out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
		}
		for ; i < n; i++ {
			out[i] = celtNorm(float32(pulses[i]) * scale)
		}
		return 1
	}

	n0 := celtUdiv(n, b)
	if n0 <= 0 {
		clear(out[:n])
		return 0
	}

	mask := 0
	base := 0
	for blk := range b {
		tmp := 0
		end := base + n0
		for i := base; i < end; i++ {
			v := pulses[i]
			out[i] = celtNorm(float32(v) * scale)
			tmp |= v
		}
		if tmp != 0 {
			mask |= 1 << blk
		}
		base = end
	}
	// Handle any remaining tail elements when n is not divisible by b.
	for i := base; i < n; i++ {
		out[i] = celtNorm(float32(pulses[i]) * scale)
	}
	return mask
}

func normalizeResidualKnownEnergyIntoAndCollapseNorm(out []celtNorm, pulses []int, gain opusVal16, energy opusVal16, b int) int {
	n := len(pulses)
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy32 := float32(energy)
	if energy32 <= 0 {
		for i := range n {
			v := float32(pulses[i])
			energy32 += v * v
		}
	}
	if energy32 <= 0 {
		clear(out[:n])
		if b <= 1 {
			return 1
		}
		return 0
	}
	scale := celtRSqrt(energy32) * float32(gain)

	if b <= 1 {
		i := 0
		for ; i+3 < n; i += 4 {
			out[i] = celtNorm(float32(pulses[i]) * scale)
			out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
			out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
			out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
		}
		for ; i < n; i++ {
			out[i] = celtNorm(float32(pulses[i]) * scale)
		}
		return 1
	}

	n0 := celtUdiv(n, b)
	if n0 <= 0 {
		clear(out[:n])
		return 0
	}

	mask := 0
	base := 0
	for blk := range b {
		tmp := 0
		end := base + n0
		for i := base; i < end; i++ {
			v := pulses[i]
			out[i] = celtNorm(float32(v) * scale)
			tmp |= v
		}
		if tmp != 0 {
			mask |= 1 << blk
		}
		base = end
	}
	for i := base; i < n; i++ {
		out[i] = celtNorm(float32(pulses[i]) * scale)
	}
	return mask
}

func normalizeResidualKnownEnergyIntoAndCollapse32(out []celtNorm, pulses []int32, gain opusVal16, energy opusVal16, b int) int {
	n := len(pulses)
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy32 := float32(energy)
	if energy32 <= 0 {
		for i := range n {
			v := float32(pulses[i])
			energy32 += v * v
		}
	}
	if energy32 <= 0 {
		clear(out[:n])
		if b <= 1 {
			return 1
		}
		return 0
	}
	scale := celtRSqrt(energy32) * float32(gain)
	if n >= 16 {
		return normalizeResidualWide(out, pulses, scale, b)
	}

	if b <= 1 {
		i := 0
		for ; i+3 < n; i += 4 {
			out[i] = celtNorm(float32(pulses[i]) * scale)
			out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
			out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
			out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
		}
		for ; i < n; i++ {
			out[i] = celtNorm(float32(pulses[i]) * scale)
		}
		return 1
	}

	n0 := celtUdiv(n, b)
	if n0 <= 0 {
		clear(out[:n])
		return 0
	}

	mask := 0
	base := 0
	for blk := range b {
		tmp := int32(0)
		end := base + n0
		i := base
		for ; i+3 < end; i += 4 {
			v0 := pulses[i]
			v1 := pulses[i+1]
			v2 := pulses[i+2]
			v3 := pulses[i+3]
			out[i] = celtNorm(float32(v0) * scale)
			out[i+1] = celtNorm(float32(v1) * scale)
			out[i+2] = celtNorm(float32(v2) * scale)
			out[i+3] = celtNorm(float32(v3) * scale)
			tmp |= v0 | v1 | v2 | v3
		}
		for ; i < end; i++ {
			v := pulses[i]
			out[i] = celtNorm(float32(v) * scale)
			tmp |= v
		}
		if tmp != 0 {
			mask |= 1 << blk
		}
		base = end
	}
	i := base
	for ; i+3 < n; i += 4 {
		out[i] = celtNorm(float32(pulses[i]) * scale)
		out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
		out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
		out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
	}
	for ; i < n; i++ {
		out[i] = celtNorm(float32(pulses[i]) * scale)
	}
	return mask
}

// normalizeResidualWide is normalizeResidualKnownEnergyIntoAndCollapse32 for
// vectors long enough for the vector scaling kernel: the pulses scale in one
// pass, then extract_collapse_mask() tests each block.
func normalizeResidualWide(out []celtNorm, pulses []int32, scale float32, b int) int {
	scalePulsesInto(out, pulses, scale)
	if b <= 1 {
		return 1
	}
	n0 := celtUdiv(len(pulses), b)
	if n0 <= 0 {
		clear(out)
		return 0
	}
	mask := 0
	blocks := pulses[:b*n0]
	for blk := range b {
		var tmp int32
		for _, v := range blocks[blk*n0 : (blk+1)*n0] {
			tmp |= v
		}
		if tmp != 0 {
			mask |= 1 << blk
		}
	}
	return mask
}

func celtRSqrt(x float32) float32 {
	return float32(1) / opusmath.SqrtF32(x)
}

func celtMul32(a, b opusVal16) opusVal16 {
	return opusVal16(float32(a) * float32(b))
}

func renormalizeVector(x []celtNorm, gain opusVal16) {
	if len(x) == 0 {
		return
	}
	n := len(x)
	x = x[:n:n]
	var energy float32
	if celtUseFusedFloatMath {
		energy = celtInnerProdNeonStyleNorm(x, x)
	} else if celtUseSSEFloatMath {
		energy = celtInnerProdSSEStyleNorm(x, x)
	} else {
		energy = renormalizeEnergy(x)
	}
	energy = float32(1e-15) + energy
	renormalizeVectorWithEnergy(x, gain, opusVal16(energy))
}

func renormalizeVectorWithEnergy(x []celtNorm, gain, energy opusVal16) {
	if len(x) == 0 || energy <= 0 {
		return
	}
	n := len(x)
	x = x[:n:n]
	_ = x[n-1]
	scale := noFMA32Mul(celtRSqrt(float32(energy)), float32(gain))
	i := 0
	for ; i+3 < n; i += 4 {
		x[i] = celtNorm(noFMA32Mul(float32(x[i]), scale))
		x[i+1] = celtNorm(noFMA32Mul(float32(x[i+1]), scale))
		x[i+2] = celtNorm(noFMA32Mul(float32(x[i+2]), scale))
		x[i+3] = celtNorm(noFMA32Mul(float32(x[i+3]), scale))
	}
	for ; i < n; i++ {
		x[i] = celtNorm(noFMA32Mul(float32(x[i]), scale))
	}
}

func scaleLowbandOutForFoldingNorm(dst []celtNorm, src []celtNorm, n int) {
	if n <= 0 {
		return
	}
	dst = dst[:n:n]
	src = src[:n:n]
	scale := opusmath.SqrtF32(float32(n))
	for i := range n {
		dst[i] = celtNorm(scale * float32(src[i]))
	}
}

// seededZeroPulseResynth fuses zero-pulse fill/fold generation with the
// exact same energy accumulation order used by renormalizeVector.
func seededZeroPulseResynth(x []celtNorm, lowband []celtNorm, seed *uint32, gain opusVal16) bool {
	if seed == nil {
		return false
	}
	n := len(x)
	if n == 0 {
		return true
	}
	x = x[:n:n]
	_ = x[n-1]

	seedVal := *seed
	// Parallelize the LCG (s = s*A + C). uint32-modular arithmetic is exactly
	// associative, so the four taps of an unrolled step can each be expressed
	// from the step-entry seed via precomputed A^k (bit-identical to the serial
	// chain), leaving only A^4 on the dependency chain (~4x shorter) and letting
	// the per-element float conversions pipeline. Powers are runtime vars because
	// A*A overflows a typed uint32 const.
	lcgA := uint32(1664525)
	lcgC := uint32(1013904223)
	a2 := lcgA * lcgA
	a3 := a2 * lcgA
	a4 := a3 * lcgA
	c2 := lcgA*lcgC + lcgC
	c3 := lcgA*c2 + lcgC
	c4 := lcgA*c3 + lcgC
	if lowband == nil {
		i := 0
		for ; i+3 < n; i += 4 {
			s1 := lcgA*seedVal + lcgC
			s2 := a2*seedVal + c2
			s3 := a3*seedVal + c3
			s4 := a4*seedVal + c4
			x[i] = celtNorm(float32(int32(s1) >> 20))
			x[i+1] = celtNorm(float32(int32(s2) >> 20))
			x[i+2] = celtNorm(float32(int32(s3) >> 20))
			x[i+3] = celtNorm(float32(int32(s4) >> 20))
			seedVal = s4
		}
		for ; i < n; i++ {
			seedVal = seedVal*lcgA + lcgC
			x[i] = celtNorm(float32(int32(seedVal) >> 20))
		}
		*seed = seedVal
		renormalizeVector(x, gain)
		return true
	}

	if len(lowband) < n {
		return false
	}
	_ = lowband[n-1]

	const foldNoise = 1.0 / 256.0
	i := 0
	for ; i+3 < n; i += 4 {
		s1 := lcgA*seedVal + lcgC
		s2 := a2*seedVal + c2
		s3 := a3*seedVal + c3
		s4 := a4*seedVal + c4
		x[i] = celtNorm(float32(lowband[i]) + float32(foldNoise)*float32(int32(((s1>>15)&1)<<1)-1))
		x[i+1] = celtNorm(float32(lowband[i+1]) + float32(foldNoise)*float32(int32(((s2>>15)&1)<<1)-1))
		x[i+2] = celtNorm(float32(lowband[i+2]) + float32(foldNoise)*float32(int32(((s3>>15)&1)<<1)-1))
		x[i+3] = celtNorm(float32(lowband[i+3]) + float32(foldNoise)*float32(int32(((s4>>15)&1)<<1)-1))
		seedVal = s4
	}
	for ; i < n; i++ {
		seedVal = seedVal*lcgA + lcgC
		x[i] = celtNorm(float32(lowband[i]) + float32(foldNoise)*float32(int32(((seedVal>>15)&1)<<1)-1))
	}
	*seed = seedVal
	renormalizeVector(x, gain)
	return true
}

func stereoMerge(x, y []celtNorm, mid opusVal16) {
	n := len(x)
	if n == 0 || len(y) < n {
		return
	}
	x = x[:n:n]
	y = y[:n:n]
	_ = x[n-1]
	_ = y[n-1]
	mid32 := float32(mid)
	xp := float32(0)
	side := float32(0)
	if celtUseFusedFloatMath {
		xp = celtInnerProdNeonStyle(y, x)
		side = celtInnerProdNeonStyle(y, y)
	} else if celtUseSSEFloatMath {
		// Both celt_inner_prod calls in one pass; each sum keeps its own
		// SSE lane order.
		xp, side = celtInnerProdSSEStylePair(y, x, y, y)
	} else {
		for i := range n {
			xv := float32(x[i])
			yv := float32(y[i])
			xp = stereoMergeScalarMAC(yv, xv, xp)
			side = stereoMergeScalarMAC(yv, yv, side)
		}
	}
	if stereoMergeUsesFMA {
		// C materializes this product before forming the energy sum; keep its
		// float32 rounding boundary ahead of the following v3 FMAs.
		xp = noFMA32Mul(xp, mid32)
	} else {
		xp *= mid32
	}
	el, er := stereoMergeEnergy(mid32, side, xp)
	if el < float32(6e-4) || er < float32(6e-4) {
		copy(y, x[:n])
		return
	}
	lgain := celtRSqrt(el)
	rgain := celtRSqrt(er)
	// The target-selected helper follows libopus contraction for the active
	// architecture: GCC v3 contracts mid*x±y before the gain multiplies.
	stereoMergeRescaleNEON(x, y, mid32, lgain, rgain)
}

func specialHybridFoldingWithEdges(norm, norm2 []celtNorm, edges []int, start, M int, dualStereo bool) {
	if start+2 >= len(edges) {
		return
	}
	n1 := M * (edges[start+1] - edges[start])
	n2 := M * (edges[start+2] - edges[start+1])
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

// algUnquantNoExtInto is libopus alg_unquant() without an extension decoder
// for len(shape) == N >= 2 dimensions and k >= 1 pulses: it decodes the PVQ
// codeword, writes it normalised to gain into shape, applies the spreading
// rotation and returns the collapse mask of the b blocks.
func algUnquantNoExtInto(shape []celtNorm, rd *rangecoding.Decoder, k, spread, b int, gain opusVal16, scratch *bandDecodeScratch) int {
	n := len(shape)
	var pulses []int32
	if scratch != nil {
		pulses = scratch.ensurePVQPulses(n)
	} else {
		pulses = make([]int32, n)
	}
	// V(N, K) = U(N, K) + U(N, K+1) from the static U table, which holds
	// zero where it does not cover a pair; covering U(N, K+1) implies
	// covering U(N, K), so a non-zero u1 is canUseCWRSFast(N, K).
	var v, u1 uint32
	if k >= n {
		if uint(n-2) < pvqUSymRows-2 && k < 255 {
			row := &pvqUSym[n&15]
			u1 = row[(k+1)&255]
			v = row[k&255] + u1
		}
	} else if uint(n) < 256 && uint(k+1) < 16 {
		col := &pvqUCol[n&255]
		u1 = col[(k+1)&15]
		v = col[k&15] + u1
	}
	table := u1 != 0
	if !table {
		v = PVQ_V(n, k)
	}
	// decode_pulses(): ec_dec_uint(dec, V), then cwrsi().
	var idx uint32
	if v <= 1<<rangecoding.EC_UINT_BITS {
		idx = rd.DecodeUniformSmall(v)
	} else {
		idx = rd.DecodeUniform(v)
	}
	// cwrsi() through decodePulsesTable32's cases, inline.
	y := pulses[:n]
	var yy uint32
	switch {
	case !table:
		yy = decodePulsesInto32(idx, n, k, y, scratch)
	case k == 1:
		finishCWRSOnePulse32(y, 0, n, idx)
		yy = 1
	case k == 2:
		yy = finishCWRSTwoPulses32(y, n, idx)
	case n == 2:
		yy = cwrsiN2(idx, k, y)
	default:
		yy = cwrsiTableLookup32(n, k, idx, y)
	}
	// normalise_residual() straight into shape (Ryy >= K > 0), the spreading
	// rotation in place, then extract_collapse_mask().
	scalePulsesInto(shape, pulses[:n], celtRSqrt(float32(yy))*float32(gain))
	expRotationNorm(shape, n, -1, b, k, spread)
	return extractCollapseMask(pulses, n, b)
}

// algUnquantInto decodes PVQ into a pre-allocated shape buffer using scratch buffers.
func algUnquantInto(shape []celtNorm, rd *rangecoding.Decoder, band, n, k, spread, b int, gain opusVal16, extDec *rangecoding.Decoder, extraBits int, scratch *bandDecodeScratch) int {
	if extraBits < 2 || extDec == nil {
		return algUnquantNoExtInto(shape[:n], rd, k, spread, b, gain, scratch)
	}

	if len(shape) < n {
		return 0
	}
	shape = shape[:n:n]
	if k <= 0 || n <= 0 {
		clear(shape)
		return 0
	}
	if rd == nil {
		clear(shape)
		return 0
	}

	vSize := PVQ_V(n, k)
	if vSize == 0 {
		clear(shape)
		return 0
	}
	var idx uint32
	if vSize <= 1<<rangecoding.EC_UINT_BITS {
		idx = rd.DecodeUniformSmall(vSize)
	} else {
		idx = rd.DecodeUniform(vSize)
	}

	var pulses []int32
	if scratch != nil {
		pulses = scratch.ensurePVQPulses(n)
	} else {
		pulses = make([]int32, n)
	}
	decodePulsesInto32(idx, n, k, pulses, scratch)
	var yy opusVal16
	up := (1 << extraBits) - 1
	up32 := int32(up)
	if n == 2 {
		refine := int32(extDec.DecodeUniform(uint32(up))) - int32((up-1)/2)
		pulses[0] *= up32
		pulses[1] *= up32
		if pulses[1] == 0 {
			if pulses[0] > 0 {
				pulses[1] = -refine
			} else {
				pulses[1] = refine
			}
			if refine*pulses[0] > 0 {
				pulses[0] -= refine
			} else {
				pulses[0] += refine
			}
		} else if pulses[1] > 0 {
			pulses[0] += refine
			if pulses[0] > 0 {
				pulses[1] -= refine
			} else {
				pulses[1] += refine
			}
		} else {
			pulses[0] -= refine
			if pulses[0] > 0 {
				pulses[1] -= refine
			} else {
				pulses[1] += refine
			}
		}
		yy = opusVal16(pvqN2Energy(float32(pulses[0]), float32(pulses[1])))
	} else {
		var refine []int32
		if scratch != nil {
			refine = scratch.ensurePVQRefine(n)
		} else {
			refine = make([]int32, n)
		}
		useEntropy := (extDec.StorageBits() - extDec.Tell()) > (n-1)*(extraBits+3)+1
		for i := 0; i < n-1; i++ {
			refine[i] = int32(ecDecRefine(extDec, up, extraBits, useEntropy))
		}
		sign := 0
		if pulses[n-1] == 0 {
			sign = int(extDec.DecodeRawBit())
		} else if pulses[n-1] < 0 {
			sign = 1
		}
		for i := 0; i < n-1; i++ {
			pulses[i] = pulses[i]*up32 + refine[i]
		}
		last := up32 * int32(k)
		for i := 0; i < n-1; i++ {
			v := pulses[i]
			if v < 0 {
				v = -v
			}
			last -= v
		}
		if sign != 0 {
			last = -last
		}
		pulses[n-1] = last
		sumSq := opusVal16(0)
		for i := range n {
			if celtUseFusedFloatMath && i < n&^3 {
				// celt/vq.c alg_unquant's paired ARM SIMD object rounds terms in
				// its vector body separately, then contracts the scalar tail.
				sumSq = opusVal16(noFMA32Add(float32(sumSq), noFMA32Mul(float32(pulses[i]), float32(pulses[i]))))
			} else {
				sumSq = opusVal16(float32(sumSq) + float32(pulses[i])*float32(pulses[i]))
			}
		}
		yy = sumSq
	}
	// Normalize and rotate directly into shape (see algUnquantNoExtInto).
	cm := normalizeResidualKnownEnergyIntoAndCollapse32(shape, pulses, opusVal16(gain), yy, b)
	expRotationNorm(shape, n, -1, b, k, spread)
	return cm
}

func algQuantScratch(re *rangecoding.Encoder, band int, x []celtNorm, n, k, spread, b int, gain opusVal16, resynth bool, extEnc *rangecoding.Encoder, extraBits int, scratch *bandEncodeScratch) int {
	if k <= 0 || n <= 0 {
		return 0
	}
	if re == nil {
		return 0
	}

	// Quantize the vector to pulses.
	var pulses []int32
	var upPulses []int32
	var collapsePulses []int32
	var refine []int32
	var yy opusVal16 // Energy computed during PVQ search
	encodedIndex := uint32(0)
	var xNorm []celtNorm
	var yy32 opusVal16
	normPath := false
	// xNormAliased is set when the no-extra-bits path works the normalized vector
	// directly in the caller's x buffer (no clone): the search, resynth and inverse
	// rotation all write x in place, so the trailing copy(x, xNorm) is a self-copy
	// and is skipped. End state of x is byte-identical to the clone+copy form.
	xNormAliased := false

	// Scratch buffer pointers
	var iyBuf *[]int32
	var signxBuf *[]byte
	var yBuf, absXBuf *[]float32
	var xNormBuf *[]celtNorm
	var uBuf *[]uint32
	if scratch != nil {
		iyBuf = &scratch.pvqIy
		signxBuf = &scratch.pvqSignx
		yBuf = &scratch.pvqY
		absXBuf = &scratch.pvqAbsX
		xNormBuf = &scratch.pvqX
		uBuf = &scratch.cwrsU
	}

	if extraBits >= 2 && extEnc != nil {
		if xNormBuf != nil {
			xNorm = ensureNormSliceNoClear(xNormBuf, n)
		} else {
			xNorm = make([]celtNorm, n)
		}
		copy(xNorm, x[:n])
		expRotationNorm(xNorm, n, 1, b, k, spread)
		normPath = true
		if n == 2 {
			var refineVal int32
			up := (1 << extraBits) - 1
			if scratch != nil {
				pulses, upPulses, refineVal, yy32 = opPVQSearchN2NormScratch(xNorm, k, up, &scratch.pvqIy, &scratch.pvqUpIy)
			} else {
				pulses, upPulses, refineVal, yy32 = opPVQSearchN2Norm(xNorm, k, up)
			}
			yy = yy32
			collapsePulses = upPulses
			index := encodePulsesFast32(pulses, n, k, uBuf)
			vSize := PVQ_V(n, k)
			if vSize == 0 {
				return 0
			}
			re.EncodeUniform(index, vSize)
			encodedIndex = index
			extEnc.EncodeUniform(uint32(refineVal+int32((up-1)/2)), uint32(up))
		} else {
			up := (1 << extraBits) - 1
			if scratch != nil {
				pulses, upPulses, refine, yy32 = opPVQSearchExtraNormScratch(
					xNorm, k, up, &scratch.pvqIy, &scratch.pvqUpIy, &scratch.pvqRefine,
					&scratch.pvqY, &scratch.pvqAbsX,
				)
			} else {
				pulses, upPulses, refine, yy32 = opPVQSearchExtraNorm(xNorm, k, up)
			}
			yy = yy32
			collapsePulses = upPulses
			index := encodePulsesFast32(pulses, n, k, uBuf)
			vSize := PVQ_V(n, k)
			if vSize == 0 {
				return 0
			}
			re.EncodeUniform(index, vSize)
			encodedIndex = index
			useEntropy := (extEnc.StorageBits() - extEnc.Tell()) > (n-1)*(extraBits+3)+1
			for i := 0; i < n-1; i++ {
				ecEncRefine(extEnc, refine[i], up, extraBits, useEntropy)
			}
			if pulses[n-1] == 0 {
				sign := 0
				if upPulses[n-1] < 0 {
					sign = 1
				}
				extEnc.EncodeRawBits(uint32(sign), 1)
			}
		}
	} else {
		// Work in place on the caller's x: forward rotation, PVQ search (with its
		// abs writeback), resynth and inverse rotation all target x directly,
		// removing the per-band clone and the trailing copy-back.
		xNorm = x[:n:n]
		xNormAliased = true
		expRotationNorm(xNorm, n, 1, b, k, spread)
		pulses, yy32 = opPVQSearchScratchNormWithInputMutation(xNorm, k, iyBuf, signxBuf, yBuf, absXBuf, true)
		yy = yy32
		collapsePulses = pulses
		normPath = true
		index := encodePulsesFast32(pulses, n, k, uBuf)
		vSize := PVQ_V(n, k)
		if vSize == 0 {
			return 0
		}
		re.EncodeUniform(index, vSize)
		encodedIndex = index
	}

	cm := 0
	if resynth {
		if len(upPulses) > 0 {
			if len(collapsePulses) > 0 {
				cm = extractCollapseMask(collapsePulses, n, b)
			}
			if normPath {
				cm = normalizeResidualKnownEnergyIntoAndCollapse32(xNorm, upPulses, opusVal16(gain), yy32, b)
				expRotationNorm(xNorm, n, -1, b, k, spread)
				copy(x[:n], xNorm)
				_ = encodedIndex
				return cm
			}
			normalizeResidualInto32(x, upPulses, gain, yy)
		} else {
			// In the common path, collapse-mask extraction and residual
			// normalization can share the same scan over the pulse vector.
			if normPath {
				cm = normalizeResidualKnownEnergyIntoAndCollapse32(xNorm, pulses, opusVal16(gain), yy32, b)
				expRotationNorm(xNorm, n, -1, b, k, spread)
				if !xNormAliased {
					copy(x[:n], xNorm)
				}
				_ = encodedIndex
				return cm
			}
			cm = normalizeResidualIntoAndCollapse32(x, pulses, gain, yy, b)
		}
		expRotation(x, n, -1, b, k, spread)
	} else if len(collapsePulses) > 0 {
		cm = extractCollapseMask(collapsePulses, n, b)
	}
	if normPath && !xNormAliased {
		copy(x[:n], xNorm)
	}
	_ = encodedIndex

	return cm
}

var computeQnExp2Table = [...]int{16384, 17866, 19483, 21247, 23170, 25267, 27554, 30048}

func computeQn(n, b, offset, pulseCap int, stereo bool) int {
	n2 := 2*n - 1
	if stereo && n == 2 {
		n2--
	}
	qb := celtSudiv(b+n2*offset, n2)
	qb = min(b-pulseCap-(4<<bitRes), qb)
	qb = min(8<<bitRes, qb)
	if qb < (1 << (bitRes - 1)) {
		return 1
	}
	qn := computeQnExp2Table[qb&0x7] >> (14 - (qb >> bitRes))
	qn = min(((qn+1)>>1)<<1, 256)
	return qn
}

// stereoItheta computes the standard 14-bit theta value for stereo encoding.
// Returns itheta in range [0, 16384].
func stereoItheta(x, y []celtNorm, stereo bool) int {
	return stereoIthetaQ30(x, y, stereo) >> 16
}

// stereoIthetaQ30 computes the extended precision Q30 theta value for stereo encoding.
// This matches libopus stereo_itheta() which returns itheta in Q30 format.
// The value represents atan2(side, mid) * 2/pi, scaled to [0, 1<<30].
// Standard itheta (14-bit) can be obtained by shifting right by 16.
func stereoIthetaQ30(x, y []celtNorm, stereo bool) int {
	// stereoIthetaQ30Norm only reads x/y (sum of squares), so no scratch copy is needed.
	return stereoIthetaQ30Norm(x, y, stereo)
}

func stereoIthetaQ30Norm(x, y []celtNorm, stereo bool) int {
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	n := min(len(y), len(x))
	if n <= 0 {
		return 0
	}
	x = x[:n:n]
	y = y[:n:n]
	_ = x[n-1]
	_ = y[n-1]

	var emid, eside float32
	if stereo {
		for i := 0; i < n; i++ {
			xv := float32(x[i])
			yv := float32(y[i])
			m := xv + yv
			s := xv - yv
			// vq.c:stereo_itheta is auto-vectorized as four-sample product
			// groups on the ARM SIMD build. Those group products round before
			// the ordered scalar adds; only the tail after the last full group
			// uses scalar FMADD. This loop has a different vector width from the
			// 16-term inner-product reductions used elsewhere in CELT.
			if celtFusedFloat && i < n&^3 {
				emid += round32(m * m)
				eside += round32(s * s)
			} else {
				emid = celtFloatMulAdd(m, m, emid)
				eside = celtFloatMulAdd(s, s, eside)
			}
		}
	} else {
		emid, eside = stereoIthetaNonStereoEnergy(x, y)
	}

	if emid <= 0 && eside <= 0 {
		return 0
	}

	// Compute mid and side magnitudes
	mid := opusmath.SqrtF32(emid)
	side := opusmath.SqrtF32(eside)
	theta := float32(0.5) + (float32(65536)*float32(16384))*celtAtan2pNormF32(side, mid)
	return floor32ToInt(theta)
}

const celtUseFusedFloatMath = celtFusedFloat
const celtUseSSEFloatMath = libopusFloatInnerProdUsesSSEOrder

func celtFloatMulAdd(a, b, c float32) float32 {
	if celtUseFusedFloatMath {
		// libopus arm/pitch_neon_intr.c:celt_inner_prod_neon forces
		// vfmaq_f32 for NEON lanes; this is the scalar lane equivalent.
		// celtUseFusedFloatMath is true on the arm64 SIMD build, where fma32
		// contracts to one FMADDS — the same single rounding as opusmath.FMA32's math.FMA
		// without its FCVT round-trips (this is a runtime-data path, so the
		// constant-folding caveat that keeps opusmath.FMA32 on math.FMA does not
		// apply).
		return fma32(a, b, c)
	}
	return a*b + c
}

func celtInnerProdSSEStyle(x, y []celtNorm) float32 {
	return celtInnerProdSSEStyleImpl(x, y)
}

func celtInnerProdSSEStyleGo(x, y []celtNorm) float32 {
	var acc [4]float32
	i := 0
	for ; i < len(x)-3; i += 4 {
		for lane := range 4 {
			product := float32(x[i+lane]) * float32(y[i+lane])
			acc[lane] += product
		}
	}
	sum0 := acc[0] + acc[2]
	sum1 := acc[1] + acc[3]
	sum := sum0 + sum1
	for ; i < len(x); i++ {
		sum = celtFloatMulAdd(float32(x[i]), float32(y[i]), sum)
	}
	return sum
}

func celtInnerProdSSEStyleNorm(x, y []celtNorm) float32 {
	return celtInnerProdSSEStyleImpl(x, y)
}

// celtInnerProdNeonStyle reproduces libopus arm/pitch_neon_intr.c
// celt_inner_prod_neon: a 4-lane vfmaq_f32 accumulator over 8-element groups,
// a 4-element tail, the (acc0+acc2)+(acc1+acc3) reduction, and a scalar tail.
// celtInnerProd8FMA32 implements this with Go kernels: arm64 SIMD uses archsimd,
// while scalar builds preserve the same fused lane and reduction order.
func celtInnerProdNeonStyle(x, y []celtNorm) float32 {
	n := min(len(y), len(x))
	return celtInnerProd8FMA32(x[:n:n], y[:n:n], n)
}

func celtInnerProdNeonStyleNorm(x, y []celtNorm) float32 {
	n := min(len(y), len(x))
	return celtInnerProd8FMA32(x[:n:n], y[:n:n], n)
}

// celtInnerProdPairLibopusOrder returns celtInnerProdLibopusOrder(x1, y1)
// and celtInnerProdLibopusOrder(x2, y2) for pairs of the same length. The two
// accumulations are independent C calls, so their chains interleave.
func celtInnerProdPairLibopusOrder(x1, y1, x2, y2 []celtNorm) (float32, float32) {
	n := min(len(y1), len(x1))
	x1, y1 = x1[:n:n], y1[:n:n]
	x2, y2 = x2[:n:n], y2[:n:n]
	if celtUseFusedFloatMath {
		return celtInnerProdNeonStyle(x1, y1), celtInnerProdNeonStyle(x2, y2)
	}
	if celtUseSSEFloatMath {
		return celtInnerProdSSEStylePair(x1, y1, x2, y2)
	}
	var s1, s2 float32
	for i := range x1 {
		s1 = celtFloatMulAdd(float32(x1[i]), float32(y1[i]), s1)
		s2 = celtFloatMulAdd(float32(x2[i]), float32(y2[i]), s2)
	}
	return s1, s2
}

func celtInnerProdLibopusOrder(x, y []celtNorm) float32 {
	n := min(len(y), len(x))
	x = x[:n:n]
	y = y[:n:n]
	if celtUseFusedFloatMath {
		return celtInnerProdNeonStyle(x, y)
	}
	if celtUseSSEFloatMath {
		return celtInnerProdSSEStyle(x, y)
	}
	var sum float32
	for i := range x {
		sum = celtFloatMulAdd(float32(x[i]), float32(y[i]), sum)
	}
	return sum
}

func celtAtanNormF32(x float32) float32 {
	const (
		a1  float32 = 0.636619772367581
		a3  float32 = -0.3333165943622589
		a5  float32 = 0.19962704181671143
		a7  float32 = -0.13976582884788513
		a9  float32 = 0.09794234484434128
		a11 float32 = -0.057773590087890625
		a13 float32 = 0.023040136322379112
		a15 float32 = -0.0043554059229791164
	)
	xSq := x * x
	p := celtFloatMulAdd(xSq, a15, a13)
	p = celtFloatMulAdd(xSq, p, a11)
	p = celtFloatMulAdd(xSq, p, a9)
	p = celtFloatMulAdd(xSq, p, a7)
	p = celtFloatMulAdd(xSq, p, a5)
	p = celtFloatMulAdd(xSq, p, a3)
	return a1 * celtFloatMulAdd(x*xSq, p, x)
}

// celtCosNorm2F32 computes cos(pi/2 * x) for x in [0, 1].
// This is used for extended precision mid/side computation from Q30 theta.
// Matches libopus celt_cos_norm2().
func celtCosNorm2F32(x float32) float32 {
	xf := x
	xf = xf - 4*float32(floor32ToInt(0.25*float32(xf+1)))
	outputSign := float32(1)
	if xf > 1 {
		outputSign = -1
		xf -= 2
	}
	const (
		cosA0 float32 = 9.999999403953552246093750000000e-01
		cosA2 float32 = -1.233698248863220214843750000000000
		cosA4 float32 = 2.536507546901702880859375000000e-01
		cosA6 float32 = -2.08106283098459243774414062500e-02
		cosA8 float32 = 8.581906440667808055877685546875e-04
	)
	xSq := xf * xf
	p := celtFloatMulAdd(xSq, cosA8, cosA6)
	p = celtFloatMulAdd(xSq, p, cosA4)
	p = celtFloatMulAdd(xSq, p, cosA2)
	p = celtFloatMulAdd(xSq, p, cosA0)
	return outputSign * p
}

func thetaSplitGains(sctx *splitCtx, useQ30 bool) (mid, side float32) {
	if sctx == nil {
		return 0, 0
	}
	if useQ30 {
		theta := float32(sctx.ithetaQ30) * (1.0 / float32(1<<30))
		return celtCosNorm2F32(theta), celtCosNorm2F32(1.0 - theta)
	}
	return float32(sctx.imid) / 32768.0, float32(sctx.iside) / 32768.0
}

func stereoSplit(x, y []celtNorm) {
	n := min(len(y), len(x))
	stereoSplitInto(x[:n], y[:n])
}

// stereoSplitInvSqrt2 is stereo_split()'s QCONST32(.70710678f,31) in the
// float build.
const stereoSplitInvSqrt2 float32 = 0.70710678

// stereoSplitScalar is the explicitly uncontracted reference loop for
// stereo_split(): it rounds both 1/sqrt(2) products before forming l+r and r-l.
func stereoSplitScalar(x, y []celtNorm) {
	y = y[:len(x)]
	for i, xv := range x {
		l := noFMA32Mul(stereoSplitInvSqrt2, float32(xv))
		r := noFMA32Mul(stereoSplitInvSqrt2, float32(y[i]))
		x[i] = celtNorm(noFMA32Add(l, r))
		y[i] = celtNorm(noFMA32Sub(r, l))
	}
}

func intensityStereoWeighted(x, y []celtNorm, leftEnergy, rightEnergy celtEner) {
	if len(x) == 0 || len(y) == 0 {
		return
	}
	n := min(len(y), len(x))
	if leftEnergy < 0 {
		leftEnergy = 0
	}
	if rightEnergy < 0 {
		rightEnergy = 0
	}
	left := float32(leftEnergy)
	right := float32(rightEnergy)
	const celtFloatEpsilon float32 = 1e-15
	norm := celtFloatEpsilon + opusmath.SqrtF32(celtFloatEpsilon+left*left+right*right)
	a1 := left / norm
	a2 := right / norm
	for i := 0; i < n; i++ {
		// Intensity stereo rounds a2*r before fusing a1*l into that product.
		x[i] = celtNorm(fma32(a1, float32(x[i]), noFMA32Mul(a2, float32(y[i]))))
	}
}

// computeChannelWeights computes channel weights for stereo RDO distortion calculation.
// This mirrors libopus bands.c compute_channel_weights().
// The weights account for inter-aural masking effects.
func computeChannelWeights(ex, ey celtEner) (w0, w1 float32) {
	ex32 := float32(ex)
	ey32 := float32(ey)
	minE := ex32
	if ey32 < minE {
		minE = ey32
	}
	// Adjustment to make the weights a bit more conservative.
	ex32 = ex32 + minE/3.0
	ey32 = ey32 + minE/3.0
	// Match libopus float path: no normalization, weights are raw adjusted energies.
	return ex32, ey32
}

func innerProductNorm(x, y []celtNorm) float32 {
	return celtInnerProdLibopusOrder(x, y)
}
