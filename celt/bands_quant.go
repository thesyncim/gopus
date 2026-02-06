package celt

import (
	"fmt"
	"math"

	"github.com/thesyncim/gopus/rangecoding"
)

// DebugStereoMerge enables tracing of stereoMerge function
var DebugStereoMerge = false

// DebugDualStereo enables tracing of dual-stereo band decoding
var DebugDualStereo = false

// Ensure fmt is used even if debug flags are false
var _ = fmt.Sprint

const (
	spreadNone       = 0
	spreadLight      = 1
	spreadNormal     = 2
	spreadAggressive = 3
)

// Exported spread constants for callers outside the celt package.
const (
	SpreadNone       = spreadNone
	SpreadLight      = spreadLight
	SpreadNormal     = spreadNormal
	SpreadAggressive = spreadAggressive
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
	re              *rangecoding.Encoder
	encode          bool
	extEnc          *rangecoding.Encoder
	extraBits       int
	bandE           []float64
	nbBands         int
	channels        int
	spread          int
	tfChange        int
	remainingBits   int
	intensity       int
	band            int
	seed            *uint32
	resynth         bool
	disableInv      bool
	avoidSplitNoise bool
	// thetaRound controls theta quantization biasing for stereo RDO:
	//   0: normal rounding (default)
	//  -1: bias toward rounding down (toward 0 or 16384)
	//   1: bias toward rounding up (toward 8192/equal split)
	// This is used by theta RDO optimization in quant_all_bands.
	thetaRound int
	// tapset controls the window taper selection for prefilter/postfilter comb filter.
	// Values: 0 (narrow), 1 (medium), 2 (wide).
	// This is set during spreading_decision and used for comb filter taper gains.
	// While not directly used in PVQ quantization, it's tracked here for
	// encoder state consistency and future prefilter integration.
	// Reference: libopus celt/celt.c comb_filter() gains table
	tapset int
	// scratch holds pre-allocated buffers for the decode hot path.
	// This eliminates per-call allocations in algUnquant, Hadamard transforms, etc.
	scratch *bandDecodeScratch
	// encScratch holds pre-allocated buffers for the encode hot path.
	// This eliminates per-call allocations in algQuant, PVQ search, etc.
	encScratch *bandEncodeScratch
}

type splitCtx struct {
	inv       int
	imid      int
	iside     int
	delta     int
	itheta    int
	ithetaQ30 int // Extended precision theta in Q30 format (when QEXT enabled)
	qalloc    int
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
	deinterleaveHadamardScratchBuf(x, n0, stride, hadamard, nil, nil)
}

func deinterleaveHadamardScratchBuf(x []float64, n0, stride int, hadamard bool, decScratch *bandDecodeScratch, encScratch *bandEncodeScratch) {
	n := n0 * stride
	var tmp []float64
	if decScratch != nil {
		tmp = decScratch.ensureHadamardTmp(n)
	} else if encScratch != nil {
		tmp = encScratch.ensureHadamardTmp(n)
	} else {
		tmp = make([]float64, n)
	}
	if hadamard {
		ordery := orderyForStride(stride)
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[ordery[i]*n0+j] = x[j*stride+i]
			}
		}
	} else {
		switch stride {
		case 2:
			for j := 0; j < n0; j++ {
				base := j << 1
				tmp[j] = x[base]
				tmp[n0+j] = x[base+1]
			}
		case 4:
			n1 := n0
			n2 := n0 << 1
			n3 := n2 + n0
			for j := 0; j < n0; j++ {
				base := j << 2
				tmp[j] = x[base]
				tmp[n1+j] = x[base+1]
				tmp[n2+j] = x[base+2]
				tmp[n3+j] = x[base+3]
			}
		case 8:
			n1 := n0
			n2 := n0 << 1
			n3 := n2 + n0
			n4 := n0 << 2
			n5 := n4 + n0
			n6 := n4 + n2
			n7 := n4 + n3
			for j := 0; j < n0; j++ {
				base := j << 3
				tmp[j] = x[base]
				tmp[n1+j] = x[base+1]
				tmp[n2+j] = x[base+2]
				tmp[n3+j] = x[base+3]
				tmp[n4+j] = x[base+4]
				tmp[n5+j] = x[base+5]
				tmp[n6+j] = x[base+6]
				tmp[n7+j] = x[base+7]
			}
		default:
			for i := 0; i < stride; i++ {
				row := i * n0
				for j := 0; j < n0; j++ {
					tmp[row+j] = x[j*stride+i]
				}
			}
		}
	}
	copy(x, tmp)
}

func interleaveHadamard(x []float64, n0, stride int, hadamard bool) {
	interleaveHadamardScratchBuf(x, n0, stride, hadamard, nil, nil)
}

func interleaveHadamardScratchBuf(x []float64, n0, stride int, hadamard bool, decScratch *bandDecodeScratch, encScratch *bandEncodeScratch) {
	n := n0 * stride
	var tmp []float64
	if decScratch != nil {
		tmp = decScratch.ensureHadamardTmp(n)
	} else if encScratch != nil {
		tmp = encScratch.ensureHadamardTmp(n)
	} else {
		tmp = make([]float64, n)
	}
	if hadamard {
		ordery := orderyForStride(stride)
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[j*stride+i] = x[ordery[i]*n0+j]
			}
		}
	} else {
		switch stride {
		case 2:
			for j := 0; j < n0; j++ {
				base := j << 1
				tmp[base] = x[j]
				tmp[base+1] = x[n0+j]
			}
		case 4:
			n1 := n0
			n2 := n0 << 1
			n3 := n2 + n0
			for j := 0; j < n0; j++ {
				base := j << 2
				tmp[base] = x[j]
				tmp[base+1] = x[n1+j]
				tmp[base+2] = x[n2+j]
				tmp[base+3] = x[n3+j]
			}
		case 8:
			n1 := n0
			n2 := n0 << 1
			n3 := n2 + n0
			n4 := n0 << 2
			n5 := n4 + n0
			n6 := n4 + n2
			n7 := n4 + n3
			for j := 0; j < n0; j++ {
				base := j << 3
				tmp[base] = x[j]
				tmp[base+1] = x[n1+j]
				tmp[base+2] = x[n2+j]
				tmp[base+3] = x[n3+j]
				tmp[base+4] = x[n4+j]
				tmp[base+5] = x[n5+j]
				tmp[base+6] = x[n6+j]
				tmp[base+7] = x[n7+j]
			}
		default:
			for i := 0; i < stride; i++ {
				row := i * n0
				for j := 0; j < n0; j++ {
					tmp[j*stride+i] = x[row+j]
				}
			}
		}
	}
	copy(x, tmp)
}

func haar1(x []float64, n0, stride int) {
	n0 >>= 1
	invSqrt2 := float32(0.7071067811865476)
	for i := 0; i < stride; i++ {
		idx0 := i
		idx1 := i + stride
		step := stride * 2
		for j := 0; j < n0; j++ {
			tmp1 := invSqrt2 * float32(x[idx0])
			tmp2 := invSqrt2 * float32(x[idx1])
			x[idx0] = float64(tmp1 + tmp2)
			x[idx1] = float64(tmp1 - tmp2)
			idx0 += step
			idx1 += step
		}
	}
}

func expRotation1(x []float64, length, stride int, c, s float64) {
	if length <= 0 {
		return
	}
	x = x[:length:length]
	_ = x[length-1]
	ms := -s

	// Hot-path specializations for common strides reduce index arithmetic while
	// preserving the same operation order as the generic implementation.
	if stride == 1 {
		end := length - 1
		i := 0
		for ; i+1 < end; i += 2 {
			x1 := x[i]
			x2 := x[i+1]
			x[i+1] = c*x2 + s*x1
			x[i] = c*x1 + ms*x2

			x3 := x[i+1]
			x4 := x[i+2]
			x[i+2] = c*x4 + s*x3
			x[i+1] = c*x3 + ms*x4
		}
		for ; i < end; i++ {
			x1 := x[i]
			x2 := x[i+1]
			x[i+1] = c*x2 + s*x1
			x[i] = c*x1 + ms*x2
		}
		i = length - 3
		for ; i-1 >= 0; i -= 2 {
			x1 := x[i]
			x2 := x[i+1]
			x[i+1] = c*x2 + s*x1
			x[i] = c*x1 + ms*x2

			x3 := x[i-1]
			x4 := x[i]
			x[i] = c*x4 + s*x3
			x[i-1] = c*x3 + ms*x4
		}
		for ; i >= 0; i-- {
			x1 := x[i]
			x2 := x[i+1]
			x[i+1] = c*x2 + s*x1
			x[i] = c*x1 + ms*x2
		}
		return
	}

	if stride == 2 {
		end := length - 2
		for i := 0; i < end; i++ {
			x1 := x[i]
			x2 := x[i+2]
			x[i+2] = c*x2 + s*x1
			x[i] = c*x1 + ms*x2
		}
		for i := length - 5; i >= 0; i-- {
			x1 := x[i]
			x2 := x[i+2]
			x[i+2] = c*x2 + s*x1
			x[i] = c*x1 + ms*x2
		}
		return
	}

	end := length - stride
	i := 0
	for ; i+1 < end; i += 2 {
		x1 := x[i]
		x2 := x[i+stride]
		x[i+stride] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2

		x3 := x[i+1]
		x4 := x[i+1+stride]
		x[i+1+stride] = c*x4 + s*x3
		x[i+1] = c*x3 + ms*x4
	}
	for ; i < end; i++ {
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
	gain := float32(length) / float32(length+spreadFactor*k)
	theta := 0.5 * gain * gain
	c := float64(float32(math.Cos(0.5 * math.Pi * float64(theta))))
	s := float64(float32(math.Sin(0.5 * math.Pi * float64(theta))))

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
	if n <= 0 {
		return 1
	}
	pulses = pulses[:n:n]
	_ = pulses[n-1]
	n0 := celtUdiv(n, b)
	mask := 0
	for i := 0; i < b; i++ {
		tmp := 0
		base := i * n0
		for j := 0; j < n0; j++ {
			tmp |= pulses[base+j]
		}
		if tmp != 0 {
			mask |= 1 << i
		}
	}
	return mask
}

// normalizeResidual normalizes the pulse vector to have the specified gain.
// If yy > 0, it uses the pre-computed energy (sum of squares) from PVQ search.
// Otherwise, it computes the energy from the pulses.
// This matches libopus normalise_residual() which receives yy as a parameter.
func normalizeResidual(pulses []int, gain float64, yy float64) []float64 {
	out := make([]float64, len(pulses))
	normalizeResidualInto(out, pulses, gain, yy)
	return out
}

// normalizeResidualInto normalizes the pulse vector into a pre-allocated output buffer.
func normalizeResidualInto(out []float64, pulses []int, gain float64, yy float64) {
	n := len(pulses)
	if len(out) < n {
		return
	}
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy := yy
	if energy <= 0 {
		// Fall back to computing energy from pulses
		i := 0
		for ; i+3 < n; i += 4 {
			v0 := pulses[i]
			v1 := pulses[i+1]
			v2 := pulses[i+2]
			v3 := pulses[i+3]
			energy += float64(v0*v0 + v1*v1 + v2*v2 + v3*v3)
		}
		for ; i < n; i++ {
			v := pulses[i]
			energy += float64(v * v)
		}
	}
	if energy <= 0 {
		clear(out[:n])
		return
	}
	scale := gain / math.Sqrt(energy)
	i := 0
	for ; i+3 < n; i += 4 {
		out[i] = float64(pulses[i]) * scale
		out[i+1] = float64(pulses[i+1]) * scale
		out[i+2] = float64(pulses[i+2]) * scale
		out[i+3] = float64(pulses[i+3]) * scale
	}
	for ; i < n; i++ {
		out[i] = float64(pulses[i]) * scale
	}
}

// normalizeResidualIntoAndCollapse normalizes the pulse vector into out and
// computes the collapse mask in the same pass.
func normalizeResidualIntoAndCollapse(out []float64, pulses []int, gain float64, yy float64, b int) int {
	n := len(pulses)
	if len(out) < n {
		return 0
	}
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy := yy
	if energy <= 0 {
		// Fall back to computing energy from pulses
		i := 0
		for ; i+3 < n; i += 4 {
			v0 := pulses[i]
			v1 := pulses[i+1]
			v2 := pulses[i+2]
			v3 := pulses[i+3]
			energy += float64(v0*v0 + v1*v1 + v2*v2 + v3*v3)
		}
		for ; i < n; i++ {
			v := pulses[i]
			energy += float64(v * v)
		}
	}
	if energy <= 0 {
		clear(out[:n])
		if b <= 1 {
			return 1
		}
		return 0
	}
	scale := gain / math.Sqrt(energy)

	if b <= 1 {
		i := 0
		for ; i+3 < n; i += 4 {
			out[i] = float64(pulses[i]) * scale
			out[i+1] = float64(pulses[i+1]) * scale
			out[i+2] = float64(pulses[i+2]) * scale
			out[i+3] = float64(pulses[i+3]) * scale
		}
		for ; i < n; i++ {
			out[i] = float64(pulses[i]) * scale
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
	for blk := 0; blk < b; blk++ {
		tmp := 0
		end := base + n0
		for i := base; i < end; i++ {
			v := pulses[i]
			out[i] = float64(v) * scale
			tmp |= v
		}
		if tmp != 0 {
			mask |= 1 << blk
		}
		base = end
	}
	// Handle any remaining tail elements when n is not divisible by b.
	for i := base; i < n; i++ {
		out[i] = float64(pulses[i]) * scale
	}
	return mask
}

func renormalizeVector(x []float64, gain float64) {
	if len(x) == 0 {
		return
	}
	n := len(x)
	x = x[:n:n]
	_ = x[n-1]
	energy := 0.0
	i := 0
	for ; i+3 < n; i += 4 {
		v0 := x[i]
		v1 := x[i+1]
		v2 := x[i+2]
		v3 := x[i+3]
		energy += v0*v0 + v1*v1 + v2*v2 + v3*v3
	}
	for ; i < n; i++ {
		v := x[i]
		energy += v * v
	}
	if energy <= 0 {
		return
	}
	scale := gain / math.Sqrt(energy)
	i = 0
	for ; i+3 < n; i += 4 {
		x[i] *= scale
		x[i+1] *= scale
		x[i+2] *= scale
		x[i+3] *= scale
	}
	for ; i < n; i++ {
		x[i] *= scale
	}
}

func stereoMerge(x, y []float64, mid float64) {
	n := len(x)
	if n == 0 || len(y) < n {
		return
	}
	x = x[:n:n]
	y = y[:n:n]
	_ = x[n-1]
	_ = y[n-1]
	xp := 0.0
	side := 0.0
	xNorm := 0.0
	i := 0
	for ; i+3 < n; i += 4 {
		x0 := x[i]
		y0 := y[i]
		x1 := x[i+1]
		y1 := y[i+1]
		x2 := x[i+2]
		y2 := y[i+2]
		x3 := x[i+3]
		y3 := y[i+3]

		xp += y0*x0 + y1*x1 + y2*x2 + y3*x3
		side += y0*y0 + y1*y1 + y2*y2 + y3*y3
		xNorm += x0*x0 + x1*x1 + x2*x2 + x3*x3
	}
	for ; i < n; i++ {
		xv := x[i]
		yv := y[i]
		xp += yv * xv
		side += yv * yv
		xNorm += xv * xv
	}
	xp *= mid
	mid2 := mid * mid
	el := mid2 + side - 2.0*xp
	er := mid2 + side + 2.0*xp
	if DebugStereoMerge {
		fmt.Printf("stereoMerge: n=%d, mid=%.6f, ||x||²=%.6f, ||y||²=%.6f, <x,y>=%.6f\n",
			n, mid, xNorm, side, xp/mid)
		fmt.Printf("  el=%.6f, er=%.6f, lgain=%.6f, rgain=%.6f\n",
			el, er, 1.0/math.Sqrt(el), 1.0/math.Sqrt(er))
	}
	if el < 6e-4 || er < 6e-4 {
		copy(y, x[:n])
		return
	}
	lgain := 1.0 / math.Sqrt(el)
	rgain := 1.0 / math.Sqrt(er)
	i = 0
	for ; i+3 < n; i += 4 {
		l0 := mid * x[i]
		r0 := y[i]
		x[i] = (l0 - r0) * lgain
		y[i] = (l0 + r0) * rgain

		l1 := mid * x[i+1]
		r1 := y[i+1]
		x[i+1] = (l1 - r1) * lgain
		y[i+1] = (l1 + r1) * rgain

		l2 := mid * x[i+2]
		r2 := y[i+2]
		x[i+2] = (l2 - r2) * lgain
		y[i+2] = (l2 + r2) * rgain

		l3 := mid * x[i+3]
		r3 := y[i+3]
		x[i+3] = (l3 - r3) * lgain
		y[i+3] = (l3 + r3) * rgain
	}
	for ; i < n; i++ {
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

// algUnquantInto decodes PVQ into a pre-allocated shape buffer using scratch buffers.
func algUnquantInto(shape []float64, rd *rangecoding.Decoder, band, n, k, spread, b int, gain float64, scratch *bandDecodeScratch) int {
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

	var pulses []int
	if scratch != nil {
		pulses = scratch.ensurePVQPulses(n)
	} else {
		pulses = make([]int, n)
	}

	vSize := PVQ_V(n, k)
	if vSize == 0 {
		clear(shape)
		return 0
	}
	idx := rd.DecodeUniform(vSize)
	yy := decodePulsesInto(idx, n, k, pulses, scratch)
	tracePVQ(band, idx, k, n, pulses)
	// CWRS decode already computes pulse energy (sum of squares), so reuse it
	// and compute collapse mask during normalization to avoid an extra pass.
	cm := normalizeResidualIntoAndCollapse(shape, pulses, gain, float64(yy), b)
	expRotation(shape, n, -1, b, k, spread)
	return cm
}

func algQuantScratch(re *rangecoding.Encoder, band int, x []float64, n, k, spread, b int, gain float64, resynth bool, extEnc *rangecoding.Encoder, extraBits int, scratch *bandEncodeScratch) int {
	if k <= 0 || n <= 0 {
		return 0
	}
	if re == nil {
		return 0
	}

	// Apply the same pre-rotation as the decoder's unquantization path.
	expRotation(x, n, 1, b, k, spread)

	// Quantize the vector to pulses.
	var pulses []int
	var upPulses []int
	var refine []int
	var yy float64 // Energy computed during PVQ search

	// Scratch buffer pointers
	var iyBuf, signxBuf *[]int
	var yBuf, absXBuf *[]float32
	var uBuf *[]uint32
	if scratch != nil {
		iyBuf = &scratch.pvqIy
		signxBuf = &scratch.pvqSignx
		yBuf = &scratch.pvqY
		absXBuf = &scratch.pvqAbsX
		uBuf = &scratch.cwrsU
	}

	if extraBits >= 2 && extEnc != nil {
		if n == 2 {
			var refineVal int
			up := (1 << extraBits) - 1
			pulses, upPulses, refineVal = opPVQSearchN2(x, k, up)
			index := EncodePulsesScratch(pulses, n, k, uBuf)
			vSize := PVQ_V(n, k)
			if vSize == 0 {
				return 0
			}
			re.EncodeUniform(index, vSize)
			extEnc.EncodeUniform(uint32(refineVal+(up-1)/2), uint32(up))
			// For extended precision, compute energy from upPulses
			yy = 0
		} else {
			up := (1 << extraBits) - 1
			pulses, upPulses, refine = opPVQSearchExtra(x, k, up)
			index := EncodePulsesScratch(pulses, n, k, uBuf)
			vSize := PVQ_V(n, k)
			if vSize == 0 {
				return 0
			}
			re.EncodeUniform(index, vSize)
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
			// For extended precision, compute energy from upPulses
			yy = 0
		}
	} else {
		pulses, yy = opPVQSearchScratch(x, k, iyBuf, signxBuf, yBuf, absXBuf)
		index := EncodePulsesScratch(pulses, n, k, uBuf)
		vSize := PVQ_V(n, k)
		if vSize == 0 {
			return 0
		}
		re.EncodeUniform(index, vSize)
	}

	cm := 0
	if len(pulses) > 0 {
		cm = extractCollapseMask(pulses, n, b)
	}

	if resynth {
		if len(upPulses) > 0 {
			// For extended precision, normalize in place
			normalizeResidualInto(x, upPulses, gain, 0)
		} else {
			// Use the energy computed during PVQ search
			normalizeResidualInto(x, pulses, gain, yy)
		}
		expRotation(x, n, -1, b, k, spread)
	}

	return cm
}

func computeQn(n, b, offset, pulseCap int, stereo bool) int {
	exp2Table := []int{16384, 17866, 19483, 21247, 23170, 25267, 27554, 30048}
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
	qn := exp2Table[qb&0x7] >> (14 - (qb >> bitRes))
	qn = ((qn + 1) >> 1) << 1
	if qn > 256 {
		qn = 256
	}
	return qn
}

// stereoItheta computes the standard 14-bit theta value for stereo encoding.
// Returns itheta in range [0, 16384].
func stereoItheta(x, y []float64, stereo bool) int {
	return stereoIthetaQ30(x, y, stereo) >> 16
}

// stereoIthetaQ30 computes the extended precision Q30 theta value for stereo encoding.
// This matches libopus stereo_itheta() which returns itheta in Q30 format.
// The value represents atan2(side, mid) * 2/pi, scaled to [0, 1<<30].
// Standard itheta (14-bit) can be obtained by shifting right by 16.
func stereoIthetaQ30(x, y []float64, stereo bool) int {
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	if n <= 0 {
		return 0
	}
	x = x[:n:n]
	y = y[:n:n]
	_ = x[n-1]
	_ = y[n-1]

	var emid, eside float64
	if stereo {
		i := 0
		for ; i+3 < n; i += 4 {
			x0 := x[i]
			y0 := y[i]
			m0 := x0 + y0
			s0 := y0 - x0

			x1 := x[i+1]
			y1 := y[i+1]
			m1 := x1 + y1
			s1 := y1 - x1

			x2 := x[i+2]
			y2 := y[i+2]
			m2 := x2 + y2
			s2 := y2 - x2

			x3 := x[i+3]
			y3 := y[i+3]
			m3 := x3 + y3
			s3 := y3 - x3

			emid += m0*m0 + m1*m1 + m2*m2 + m3*m3
			eside += s0*s0 + s1*s1 + s2*s2 + s3*s3
		}
		for ; i < n; i++ {
			m := x[i] + y[i]
			s := y[i] - x[i]
			emid += m * m
			eside += s * s
		}
	} else {
		i := 0
		for ; i+3 < n; i += 4 {
			x0 := x[i]
			x1 := x[i+1]
			x2 := x[i+2]
			x3 := x[i+3]
			y0 := y[i]
			y1 := y[i+1]
			y2 := y[i+2]
			y3 := y[i+3]
			emid += x0*x0 + x1*x1 + x2*x2 + x3*x3
			eside += y0*y0 + y1*y1 + y2*y2 + y3*y3
		}
		for ; i < n; i++ {
			emid += x[i] * x[i]
			eside += y[i] * y[i]
		}
	}

	if emid <= 0 && eside <= 0 {
		return 0
	}

	// Compute mid and side magnitudes
	mid := math.Sqrt(emid)
	side := math.Sqrt(eside)

	// Compute atan2(side, mid) * 2/pi in Q30 format
	// This matches libopus: itheta = floor(0.5 + 65536 * 16384 * atan2p_norm(side, mid))
	// where atan2p_norm returns atan2(y,x) * 2/pi
	atan2pNorm := celtAtan2pNorm(side, mid)

	// Scale to Q30: multiply by 2^30 (1073741824)
	// libopus float path: itheta = (int)floor(.5f + 65536.f * 16384 * celt_atan2p_norm(side, mid))
	// 65536 * 16384 = 2^30
	ithetaQ30 := int(math.Floor(0.5 + 1073741824.0*atan2pNorm))
	if ithetaQ30 < 0 {
		ithetaQ30 = 0
	}
	if ithetaQ30 > 1073741824 { // 1 << 30
		ithetaQ30 = 1073741824
	}
	return ithetaQ30
}

// celtAtan2pNorm computes atan2(y, x) * 2/pi for non-negative x, y.
// Returns a value in [0, 1] representing the angle as a fraction of pi/2.
func celtAtan2pNorm(y, x float64) float64 {
	// For very small values, return 0
	if (x*x + y*y) < 1e-18 {
		return 0
	}

	if y < x {
		return celtAtanNorm(y / x)
	}
	return 1.0 - celtAtanNorm(x/y)
}

// celtAtanNorm computes atan(x) * 2/pi using polynomial approximation.
// Matches libopus celt_atan_norm() for float path.
func celtAtanNorm(x float64) float64 {
	// Coefficients from libopus mathops.h for atan approximation
	// Using Taylor series: atan(x) ≈ x - x^3/3 + x^5/5 - x^7/7 + ...
	// Scaled by 2/pi
	const (
		a1  = 0.6366197723675814 // 2/pi
		a3  = -0.2122065907891938
		a5  = 0.1272767503321694
		a7  = -0.09090395389159065
		a9  = 0.06622438065498507
		a11 = -0.04393921727468699
		a13 = 0.02173787448476704
		a15 = -0.005765602298498684
	)

	xSq := x * x
	return x * (a1 + xSq*(a3+xSq*(a5+xSq*(a7+xSq*(a9+xSq*(a11+xSq*(a13+xSq*a15)))))))
}

// celtCosNorm2 computes cos(pi/2 * x) for x in [0, 1].
// This is used for extended precision mid/side computation from Q30 theta.
// Matches libopus celt_cos_norm2().
func celtCosNorm2(x float64) float64 {
	// Restrict x to [-1, 3] then to [-1, 1]
	x -= 4 * math.Floor(0.25*(x+1))
	outputSign := 1.0
	if x > 1 {
		outputSign = -1.0
		x -= 2
	}

	// Polynomial coefficients from libopus
	const (
		cosA0 = 9.999999403953552246093750000000e-01
		cosA2 = -1.233698248863220214843750000000
		cosA4 = 2.536507546901702880859375000000e-01
		cosA6 = -2.08106283098459243774414062500e-02
		cosA8 = 8.581906440667808055877685546875e-04
	)

	xSq := x * x
	return outputSign * (cosA0 + xSq*(cosA2+xSq*(cosA4+xSq*(cosA6+xSq*cosA8))))
}

func stereoSplit(x, y []float64) {
	if len(x) == 0 || len(y) == 0 {
		return
	}
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	invSqrt2 := 1.0 / math.Sqrt(2.0)
	for i := 0; i < n; i++ {
		l := x[i] * invSqrt2
		r := y[i] * invSqrt2
		x[i] = l + r
		y[i] = r - l
	}
}

func intensityStereoWeighted(x, y []float64, leftEnergy, rightEnergy float64) {
	if len(x) == 0 || len(y) == 0 {
		return
	}
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	if leftEnergy < 0 {
		leftEnergy = 0
	}
	if rightEnergy < 0 {
		rightEnergy = 0
	}
	norm := math.Sqrt(leftEnergy*leftEnergy + rightEnergy*rightEnergy)
	if norm <= 0 {
		return
	}
	a1 := leftEnergy / norm
	a2 := rightEnergy / norm
	for i := 0; i < n; i++ {
		x[i] = a1*x[i] + a2*y[i]
	}
}

// computeChannelWeights computes channel weights for stereo RDO distortion calculation.
// This mirrors libopus bands.c compute_channel_weights().
// The weights account for inter-aural masking effects.
func computeChannelWeights(ex, ey float64) (w0, w1 float64) {
	minE := ex
	if ey < minE {
		minE = ey
	}
	// Adjustment to make the weights a bit more conservative.
	ex = ex + minE/3.0
	ey = ey + minE/3.0
	// Match libopus float path: no normalization, weights are raw adjusted energies.
	return ex, ey
}

// innerProduct computes the inner product of two vectors.
// Used for distortion measurement in theta RDO.
func innerProduct(x, y []float64) float64 {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += x[i] * y[i]
	}
	return sum
}

func (ctx *bandCtx) bandEnergy(channel int) float64 {
	if ctx.bandE == nil || ctx.nbBands <= 0 || channel < 0 || channel >= ctx.channels {
		return 0
	}
	idx := channel*ctx.nbBands + ctx.band
	if idx < 0 || idx >= len(ctx.bandE) {
		return 0
	}
	return ctx.bandE[idx]
}

// computeTheta computes and encodes/decodes the stereo theta angle.
// This is the standard version without extended precision support.
func computeTheta(ctx *bandCtx, sctx *splitCtx, x, y []float64, n int, b *int, B, B0, lm int, stereo bool, fill *int) {
	if !ctx.encode {
		computeThetaDecode(ctx, sctx, x, y, n, b, B, B0, lm, stereo, fill)
		return
	}
	computeThetaExt(ctx, sctx, x, y, n, b, nil, B, B0, lm, stereo, fill)
}

func computeThetaDecode(ctx *bandCtx, sctx *splitCtx, x, y []float64, n int, b *int, B, B0, lm int, stereo bool, fill *int) {
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
	ithetaQ30 := 0
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
		ithetaQ30 = itheta << 16
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
		ithetaQ30 = 0
	}

	qalloc := 0
	if ctx.rd != nil {
		qalloc = ctx.rd.TellFrac() - tell
	}
	if qalloc != 0 {
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
		delta = fracMul16((n-1)<<7, bitexactLog2tanTheta(itheta))
	}

	sctx.inv = inv
	sctx.imid = imid
	sctx.iside = iside
	sctx.delta = delta
	sctx.itheta = itheta
	sctx.ithetaQ30 = ithetaQ30
}

// computeThetaExt computes and encodes/decodes the stereo theta angle with optional extended precision.
// When extended precision is available (ctx.extEnc != nil and extB != nil),
// it also encodes additional Q30 precision bits to the extension bitstream.
// Reference: libopus bands.c compute_theta() with ENABLE_QEXT path (lines 863-885)
func computeThetaExt(ctx *bandCtx, sctx *splitCtx, x, y []float64, n int, b *int, extB *int, B, B0, lm int, stereo bool, fill *int) {
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
	if ctx.encode {
		if ctx.re != nil {
			tell = ctx.re.TellFrac()
		}
	} else if ctx.rd != nil {
		tell = ctx.rd.TellFrac()
	}
	itheta := 0
	ithetaQ30 := 0
	inv := 0
	if qn != 1 {
		if ctx.encode {
			// Compute Q30 precision theta for extended encoding
			ithetaQ30 = stereoIthetaQ30(x, y, stereo)
			itheta = ithetaQ30 >> 16

			// Apply theta biasing for stereo RDO.
			// When thetaRound == 0: normal rounding (default)
			// When thetaRound != 0: bias toward 0/16384 (away from equal split)
			// Reference: libopus bands.c compute_theta(), lines 787-796
			if !stereo || ctx.thetaRound == 0 {
				// Standard rounding
				itheta = (itheta*qn + 8192) >> 14
				if !stereo && ctx.avoidSplitNoise && itheta > 0 && itheta < qn {
					unquantized := celtUdiv(itheta*16384, qn)
					delta := fracMul16((n-1)<<7, bitexactLog2tanTheta(unquantized))
					if delta > *b {
						itheta = qn
					} else if delta < -*b {
						itheta = 0
					}
				}
			} else {
				// Stereo theta RDO biasing: bias toward 0 or 16384
				// (away from equal split at itheta=8192).
				// bias is positive when itheta > 8192, negative otherwise
				bias := 0
				if itheta > 8192 {
					bias = 32767 / qn
				} else {
					bias = -32767 / qn
				}
				down := (itheta*qn + bias) >> 14
				if down < 0 {
					down = 0
				}
				if down > qn-1 {
					down = qn - 1
				}
				if ctx.thetaRound < 0 {
					itheta = down
				} else {
					itheta = down + 1
				}
			}
		}
		if stereo && n > 2 {
			p0 := 3
			x0 := qn / 2
			ft := p0*(x0+1) + x0
			if ctx.encode {
				if ctx.re != nil {
					var fl, fs int
					if itheta <= x0 {
						fl = p0 * itheta
						fs = p0
					} else {
						fl = (itheta - 1 - x0) + (x0+1)*p0
						fs = 1
					}
					ctx.re.Encode(uint32(fl), uint32(fl+fs), uint32(ft))
				}
			} else if ctx.rd != nil {
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
			if ctx.encode {
				if ctx.re != nil {
					ctx.re.EncodeUniform(uint32(itheta), uint32(qn+1))
				}
			} else if ctx.rd != nil {
				itheta = int(ctx.rd.DecodeUniform(uint32(qn + 1)))
			}
		} else {
			ft := ((qn >> 1) + 1) * ((qn >> 1) + 1)
			if ctx.encode {
				if ctx.re != nil {
					fs := 1
					fl := 0
					if itheta <= (qn >> 1) {
						fs = itheta + 1
						fl = itheta * (itheta + 1) >> 1
					} else {
						fs = qn + 1 - itheta
						fl = ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1)
					}
					ctx.re.Encode(uint32(fl), uint32(fl+fs), uint32(ft))
				}
			} else if ctx.rd != nil {
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
		// Unquantize itheta to 14-bit range [0, 16384]
		itheta = celtUdiv(itheta*16384, qn)

		// Extended precision theta encoding (ENABLE_QEXT path in libopus)
		// Reference: libopus bands.c lines 863-885
		if stereo && ctx.encode && ctx.extEnc != nil && extB != nil {
			// Get available extension bits
			extTotalBits := ctx.extEnc.StorageBits()
			extTell := ctx.extEnc.TellFrac()
			availExtBits := extTotalBits - extTell

			// Check if we have enough bits for extended precision
			// Condition: ext_b >= 2*N<<BITRES && ext_total_bits - ec_tell_frac(ext_ec) - 1 > 2<<BITRES
			if *extB >= 2*n<<bitRes && availExtBits-1 > 2<<bitRes {
				extTellBefore := ctx.extEnc.TellFrac()

				// Compute number of extra bits: min(12, max(2, ext_b / ((2*N-1)<<BITRES)))
				extraBits := celtSudiv(*extB, (2*n-1)<<bitRes)
				if extraBits < 2 {
					extraBits = 2
				}
				if extraBits > 12 {
					extraBits = 12
				}

				// Encode extended precision theta
				// itheta_q30 = itheta_q30 - (itheta << 16)
				// This gives the fractional part that wasn't captured in itheta
				residual := ithetaQ30 - (itheta << 16)

				// Scale to the range of extra bits:
				// itheta_q30 = (residual * qn * ((1<<extra_bits)-1) + (1<<29)) >> 30
				scaleFactor := int64(qn) * int64((1<<extraBits)-1)
				encodedVal := int((int64(residual)*scaleFactor + (1 << 29)) >> 30)

				// Add bias to center the range
				encodedVal += (1 << (extraBits - 1)) - 1

				// Clamp to valid range [0, (1<<extraBits)-2]
				if encodedVal < 0 {
					encodedVal = 0
				}
				if encodedVal > (1<<extraBits)-2 {
					encodedVal = (1 << extraBits) - 2
				}

				// Encode using uniform distribution
				ctx.extEnc.EncodeUniform(uint32(encodedVal), uint32((1<<extraBits)-1))

				// Update ext_b with bits consumed
				*extB -= ctx.extEnc.TellFrac() - extTellBefore
			}
		}

		// Set ithetaQ30 for output - use extended precision if computed, else fall back to standard
		if ithetaQ30 == 0 || !ctx.encode {
			ithetaQ30 = itheta << 16
		}

		if ctx.encode && stereo {
			if itheta == 0 {
				intensityStereoWeighted(x, y, ctx.bandEnergy(0), ctx.bandEnergy(1))
			} else {
				stereoSplit(x, y)
			}
		}
	} else if stereo {
		if *b > 2<<bitRes && ctx.remainingBits > 2<<bitRes {
			if ctx.encode {
				if ctx.re != nil {
					ctx.re.EncodeBit(0, 2)
				}
			} else if ctx.rd != nil {
				inv = ctx.rd.DecodeBit(2)
			}
		}
		if ctx.disableInv {
			inv = 0
		}
		if ctx.encode && stereo && inv != 0 && y != nil {
			for i := range y {
				y[i] = -y[i]
			}
		}
		if ctx.encode && stereo {
			intensityStereoWeighted(x, y, ctx.bandEnergy(0), ctx.bandEnergy(1))
		}
		itheta = 0
		ithetaQ30 = 0
	}

	qalloc := 0
	if ctx.encode {
		if ctx.re != nil {
			qalloc = ctx.re.TellFrac() - tell
		}
	} else if ctx.rd != nil {
		qalloc = ctx.rd.TellFrac() - tell
	}
	if qalloc != 0 {
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
		delta = fracMul16((n-1)<<7, bitexactLog2tanTheta(itheta))
	}

	sctx.inv = inv
	sctx.imid = imid
	sctx.iside = iside
	sctx.delta = delta
	sctx.itheta = itheta
	sctx.ithetaQ30 = ithetaQ30
}

func quantPartition(ctx *bandCtx, x []float64, n, b, B int, lowband []float64, lm int, gain float64, fill int) (int, []float64) {
	if n == 1 {
		return 1, x
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	var lut *pulseCacheLUT
	maxBits := 0
	if l := pulseCacheLUTForBand(ctx.band, lm); l != nil {
		lut = l
		if lm != -1 {
			maxBits = l.maxBits
		}
	}

	if lm != -1 && b > maxBits+12 && n > 2 {
		nHalf := n >> 1
		y := x[nHalf:]
		lm--
		B0 := B
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		sctx := splitCtx{}
		computeTheta(ctx, &sctx, x[:nHalf], y, nHalf, &b, B, B0, lm, false, &fill)
		mid := float64(sctx.imid) / 32768.0
		side := float64(sctx.iside) / 32768.0
		if B0 > 1 && (sctx.itheta&0x3fff) != 0 {
			if sctx.itheta > 8192 {
				sctx.delta -= sctx.delta >> (4 - lm)
			} else {
				sctx.delta = min(0, sctx.delta+(nHalf<<bitRes>>(5-lm)))
			}
		}
		mbits := max(0, min(b, (b-sctx.delta)/2))
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
			cm |= scm << (B0 >> 1)
		} else {
			cm, _ = quantPartition(ctx, y, nHalf, sbits, B, lowband2, lm, gain*side, fill>>B)
			cm <<= B0 >> 1
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			scm, _ := quantPartition(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, gain*mid, fill)
			cm |= scm
		}
		return cm, x
	}

	q := 0
	currBits := 0
	if lut != nil {
		if b > 0 {
			if b < len(lut.bitsToPulses) {
				q = int(lut.bitsToPulses[b])
			} else {
				q = lut.maxPseudo
			}
			currBits = pulsesToBitsCached(lut.cache, q)
			ctx.remainingBits -= currBits
			for ctx.remainingBits < 0 && q > 0 {
				ctx.remainingBits += currBits
				q--
				currBits = pulsesToBitsCached(lut.cache, q)
				ctx.remainingBits -= currBits
			}
		}
	} else {
		q = bitsToPulses(ctx.band, lm, b)
		currBits = pulsesToBits(ctx.band, lm, q)
		ctx.remainingBits -= currBits
		for ctx.remainingBits < 0 && q > 0 {
			ctx.remainingBits += currBits
			q--
			currBits = pulsesToBits(ctx.band, lm, q)
			ctx.remainingBits -= currBits
		}
	}
	if q != 0 {
		k := getPulses(q)
		if ctx.encode {
			cm := algQuantScratch(ctx.re, ctx.band, x, n, k, ctx.spread, B, gain, ctx.resynth, ctx.extEnc, ctx.extraBits, ctx.encScratch)
			return cm, x
		}
		// Use scratch-aware version to avoid allocations in decode hot path
		cm := algUnquantInto(x, ctx.rd, ctx.band, n, k, ctx.spread, B, gain, ctx.scratch)
		return cm, x
	}

	if ctx.resynth {
		cmMask := (1 << B) - 1
		fill &= cmMask
		if fill == 0 {
			clear(x)
			return 0, x
		}
		if lowband == nil {
			if ctx.seed != nil {
				for i := range x {
					*ctx.seed = (*ctx.seed)*1664525 + 1013904223
					// Match libopus: arithmetic shift on signed seed before scaling.
					x[i] = float64(int32(*ctx.seed) >> 20)
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

func quantPartitionDecode(ctx *bandCtx, x []float64, n, b, B int, lowband []float64, lm int, gain float64, fill int) int {
	if n == 1 {
		return 1
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	var lut *pulseCacheLUT
	maxBits := 0
	if l := pulseCacheLUTForBand(ctx.band, lm); l != nil {
		lut = l
		if lm != -1 {
			maxBits = l.maxBits
		}
	}

	if lm != -1 && b > maxBits+12 && n > 2 {
		nHalf := n >> 1
		y := x[nHalf:]
		lm--
		B0 := B
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		sctx := splitCtx{}
		computeThetaDecode(ctx, &sctx, x[:nHalf], y, nHalf, &b, B, B0, lm, false, &fill)
		mid := float64(sctx.imid) / 32768.0
		side := float64(sctx.iside) / 32768.0
		if B0 > 1 && (sctx.itheta&0x3fff) != 0 {
			if sctx.itheta > 8192 {
				sctx.delta -= sctx.delta >> (4 - lm)
			} else {
				sctx.delta = min(0, sctx.delta+(nHalf<<bitRes>>(5-lm)))
			}
		}
		mbits := max(0, min(b, (b-sctx.delta)/2))
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
			cm = quantPartitionDecode(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, gain*mid, fill)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			scm := quantPartitionDecode(ctx, y, nHalf, sbits, B, lowband2, lm, gain*side, fill>>B)
			cm |= scm << (B0 >> 1)
		} else {
			cm = quantPartitionDecode(ctx, y, nHalf, sbits, B, lowband2, lm, gain*side, fill>>B)
			cm <<= B0 >> 1
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			scm := quantPartitionDecode(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, gain*mid, fill)
			cm |= scm
		}
		return cm
	}

	q := 0
	currBits := 0
	if lut != nil {
		if b > 0 {
			if b < len(lut.bitsToPulses) {
				q = int(lut.bitsToPulses[b])
			} else {
				q = lut.maxPseudo
			}
			currBits = pulsesToBitsCached(lut.cache, q)
			ctx.remainingBits -= currBits
			for ctx.remainingBits < 0 && q > 0 {
				ctx.remainingBits += currBits
				q--
				currBits = pulsesToBitsCached(lut.cache, q)
				ctx.remainingBits -= currBits
			}
		}
	} else {
		q = bitsToPulses(ctx.band, lm, b)
		currBits = pulsesToBits(ctx.band, lm, q)
		ctx.remainingBits -= currBits
		for ctx.remainingBits < 0 && q > 0 {
			ctx.remainingBits += currBits
			q--
			currBits = pulsesToBits(ctx.band, lm, q)
			ctx.remainingBits -= currBits
		}
	}
	if q != 0 {
		k := getPulses(q)
		cm := algUnquantInto(x, ctx.rd, ctx.band, n, k, ctx.spread, B, gain, ctx.scratch)
		return cm
	}

	if ctx.resynth {
		cmMask := (1 << B) - 1
		fill &= cmMask
		if fill == 0 {
			clear(x)
			return 0
		}
		if lowband == nil {
			if ctx.seed != nil {
				for i := range x {
					*ctx.seed = (*ctx.seed)*1664525 + 1013904223
					x[i] = float64(int32(*ctx.seed) >> 20)
				}
			}
			renormalizeVector(x, gain)
			return cmMask
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
		return fill
	}
	return fill
}
func quantBandN1(ctx *bandCtx, x, y []float64, b int, lowbandOut []float64) int {
	stereo := y != nil
	x0 := x
	for c := 0; c < 1+boolToInt(stereo); c++ {
		sign := 0
		if ctx.remainingBits >= 1<<bitRes {
			if ctx.encode {
				if ctx.re != nil {
					if x[0] < 0 {
						sign = 1
					}
					ctx.re.EncodeRawBits(uint32(sign), 1)
				}
			} else if ctx.rd != nil {
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
		// In floating-point mode, libopus's SHR32(X[0],4) is a no-op,
		// so we just copy the value directly without any scaling.
		lowbandOut[0] = x0[0]
	}
	return 1
}

func quantBandN1Decode(ctx *bandCtx, x, y []float64, b int, lowbandOut []float64) int {
	if y == nil {
		return quantBandN1DecodeMono(ctx, x, b, lowbandOut)
	}
	return quantBandN1DecodeStereo(ctx, x, y, b, lowbandOut)
}

func quantBandN1DecodeMono(ctx *bandCtx, x []float64, b int, lowbandOut []float64) int {
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
	if lowbandOut != nil && len(lowbandOut) > 0 {
		// In floating-point mode, libopus's SHR32(X[0],4) is a no-op.
		lowbandOut[0] = x[0]
	}
	return 1
}

func quantBandN1DecodeStereo(ctx *bandCtx, x, y []float64, b int, lowbandOut []float64) int {
	sign0 := 0
	if ctx.remainingBits >= 1<<bitRes {
		if ctx.rd != nil {
			sign0 = int(ctx.rd.DecodeRawBits(1))
		}
		ctx.remainingBits -= 1 << bitRes
	}
	sign1 := 0
	if ctx.remainingBits >= 1<<bitRes {
		if ctx.rd != nil {
			sign1 = int(ctx.rd.DecodeRawBits(1))
		}
		ctx.remainingBits -= 1 << bitRes
	}
	if ctx.resynth {
		val0 := 1.0
		if sign0 != 0 {
			val0 = -1.0
		}
		val1 := 1.0
		if sign1 != 0 {
			val1 = -1.0
		}
		x[0] = val0
		y[0] = val1
	}
	if lowbandOut != nil && len(lowbandOut) > 0 {
		// In floating-point mode, libopus's SHR32(X[0],4) is a no-op.
		lowbandOut[0] = x[0]
	}
	return 1
}

func quantBand(ctx *bandCtx, x []float64, n, b, B int, lowband []float64, lm int, lowbandOut []float64, gain float64, lowbandScratch []float64, fill int) int {
	if n == 1 {
		return quantBandN1(ctx, x, nil, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
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
			if ctx.encode {
				haar1(x, n>>k, 1<<k)
			}
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
		if ctx.encode {
			haar1(x, N_B, B)
		}
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
		if ctx.encode {
			deinterleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
		if lowband != nil {
			deinterleaveHadamardScratchBuf(lowband, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
	}

	cm, _ := quantPartition(ctx, x, n, b, B, lowband, lm, gain, fill)

	if ctx.resynth {
		if B0 > 1 {
			interleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
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

func quantBandDecode(ctx *bandCtx, x []float64, n, b, B int, lowband []float64, lm int, lowbandOut []float64, gain float64, lowbandScratch []float64, fill int) int {
	if n == 1 {
		return quantBandN1Decode(ctx, x, nil, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
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
		if lowband != nil {
			deinterleaveHadamardScratchBuf(lowband, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
	}

	cm := quantPartitionDecode(ctx, x, n, b, B, lowband, lm, gain, fill)

	if ctx.resynth {
		if B0 > 1 {
			interleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
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
		return quantBandN1(ctx, x, y, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
		if y != nil {
			y = y[:n:n]
			_ = y[n-1]
		}
	}

	origFill := fill

	if ctx.encode && ctx.bandE != nil && ctx.channels == 2 {
		l := ctx.bandEnergy(0)
		r := ctx.bandEnergy(1)
		if l < 1e-10 || r < 1e-10 {
			if l > r {
				copy(y, x)
			} else {
				copy(x, y)
			}
		}
	}

	sctx := splitCtx{}
	computeTheta(ctx, &sctx, x, y, n, &b, B, B, lm, true, &fill)
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
		if sbits > 0 {
			if ctx.encode {
				if ctx.re != nil {
					bit := 0
					if x2[0]*y2[1]-x2[1]*y2[0] < 0 {
						bit = 1
					}
					ctx.re.EncodeRawBits(uint32(bit), 1)
					if bit != 0 {
						sign = -1
					}
				}
			} else if ctx.rd != nil {
				if ctx.rd.DecodeRawBits(1) == 1 {
					sign = -1
				}
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
			// Apply inv negation (same as common resynth path)
			if sctx.inv != 0 {
				y[0] = -y[0]
				y[1] = -y[1]
			}
		}
		return cm
	}

	delta := sctx.delta
	mbits := max(0, min(b, (b-delta)/2))
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

func quantBandStereoDecode(ctx *bandCtx, x, y []float64, n, b, B int, lowband []float64, lm int, lowbandOut []float64, lowbandScratch []float64, fill int) int {
	if n == 1 {
		return quantBandN1Decode(ctx, x, y, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
		if y != nil {
			y = y[:n:n]
			_ = y[n-1]
		}
	}

	origFill := fill

	sctx := splitCtx{}
	computeThetaDecode(ctx, &sctx, x, y, n, &b, B, B, lm, true, &fill)
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
		if sbits > 0 {
			if ctx.rd != nil {
				if ctx.rd.DecodeRawBits(1) == 1 {
					sign = -1
				}
			}
		}
		cm := quantBandDecode(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, origFill)
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
			if sctx.inv != 0 {
				y[0] = -y[0]
				y[1] = -y[1]
			}
		}
		return cm
	}

	delta := sctx.delta
	mbits := max(0, min(b, (b-delta)/2))
	sbits := b - mbits
	ctx.remainingBits -= sctx.qalloc

	rebalance := ctx.remainingBits
	cm := 0
	if mbits >= sbits {
		cm = quantBandDecode(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill)
		rebalance = mbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 0 {
			sbits += rebalance - (3 << bitRes)
		}
		cm |= quantBandDecode(ctx, y, n, sbits, B, nil, lm, nil, side, nil, fill>>B)
	} else {
		cm = quantBandDecode(ctx, y, n, sbits, B, nil, lm, nil, side, nil, fill>>B)
		rebalance = sbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 16384 {
			mbits += rebalance - (3 << bitRes)
		}
		cm |= quantBandDecode(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill)
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

func quantAllBandsDecodeWithScratch(rd *rangecoding.Decoder, channels, frameSize, lm int, start, end int,
	pulses []int, shortBlocks int, spread int, dualStereo, intensity int,
	tfRes []int, totalBitsQ3 int, balance int, codedBands int, disableInv bool, seed *uint32,
	scratch *bandDecodeScratch) (left, right []float64, collapse []byte) {
	if DebugDualStereo {
		fmt.Printf("quantAllBandsDecode: dualStereo=%d, intensity=%d, channels=%d, start=%d, end=%d\n",
			dualStereo, intensity, channels, start, end)
	}
	M := 1 << lm
	B := 1
	if shortBlocks > 1 {
		B = shortBlocks
	}
	N := frameSize
	if scratch == nil {
		left = make([]float64, N)
		if channels == 2 {
			right = make([]float64, N)
		}
		collapse = make([]byte, channels*MaxBands)
	} else {
		left = ensureFloat64Slice(&scratch.left, N)
		for i := range left {
			left[i] = 0
		}
		if channels == 2 {
			right = ensureFloat64Slice(&scratch.right, N)
			for i := range right {
				right[i] = 0
			}
		} else if cap(scratch.right) > 0 {
			scratch.right = scratch.right[:0]
			right = nil
		}
		collapse = ensureByteSlice(&scratch.collapse, channels*MaxBands)
		for i := range collapse {
			collapse[i] = 0
		}
	}

	normOffset := M * EBands[start]
	normLen := M*EBands[MaxBands-1] - normOffset
	if normLen < 0 {
		normLen = 0
	}
	var norm []float64
	if scratch == nil {
		norm = make([]float64, channels*normLen)
	} else {
		norm = ensureFloat64Slice(&scratch.norm, channels*normLen)
		for i := range norm {
			norm[i] = 0
		}
	}
	var norm2 []float64
	if channels == 2 {
		norm2 = norm[normLen:]
	}

	maxBand := M * (EBands[end] - EBands[end-1])
	var lowbandScratch []float64
	if scratch == nil {
		lowbandScratch = make([]float64, maxBand)
	} else {
		lowbandScratch = ensureFloat64Slice(&scratch.lowband, maxBand)
	}

	lowbandOffset := 0
	updateLowband := true
	ctx := bandCtx{
		rd:              rd,
		spread:          spread,
		remainingBits:   0,
		intensity:       intensity,
		seed:            seed,
		resynth:         true,
		disableInv:      disableInv,
		avoidSplitNoise: B > 1,
		scratch:         scratch,
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
		currBalance := 0
		if i <= codedBands-1 {
			currBalance = celtSudiv(balance, min(3, codedBands-i))
			b = max(0, min(16383, min(remaining+1, pulses[i]+currBalance)))
		}
		if ctx.resynth && (M*EBands[i]-nBand >= M*EBands[start] || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		if i == start+1 {
			specialHybridFolding(norm, norm2, start, M, dualStereo != 0)
		}

		ctx.tfChange = tfRes[i]

		effectiveLowband := -1
		xCM := 0
		yCM := 0
		if lowbandOffset != 0 && (spread != spreadAggressive || B > 1 || ctx.tfChange < 0) {
			effectiveLowband = max(0, M*EBands[lowbandOffset]-normOffset-nBand)
			foldStart := lowbandOffset
			for {
				foldStart--
				if foldStart <= start {
					foldStart = start
					break
				}
				if M*EBands[foldStart] <= effectiveLowband+normOffset {
					break
				}
			}
			foldEnd := lowbandOffset - 1
			for {
				foldEnd++
				if foldEnd >= i {
					break
				}
				if M*EBands[foldEnd] >= effectiveLowband+normOffset+nBand {
					break
				}
			}
			for fold := foldStart; fold < foldEnd; fold++ {
				xCM |= int(collapse[fold*channels])
				if channels == 2 {
					yCM |= int(collapse[fold*channels+channels-1])
				}
			}
		} else {
			xCM = (1 << B) - 1
			yCM = xCM
		}

		if dualStereo != 0 && i == intensity {
			dualStereo = 0
			if ctx.resynth {
				mergeLimit := M*EBands[i] - normOffset
				if mergeLimit < 0 {
					mergeLimit = 0
				}
				if mergeLimit > len(norm) {
					mergeLimit = len(norm)
				}
				if channels == 2 && mergeLimit > len(norm2) {
					mergeLimit = len(norm2)
				}
				for j := 0; j < mergeLimit; j++ {
					norm[j] = 0.5 * (norm[j] + norm2[j])
				}
			}
		}

		var lowbandX []float64
		var lowbandY []float64
		if effectiveLowband >= 0 && effectiveLowband+nBand <= len(norm) {
			lowbandX = norm[effectiveLowband : effectiveLowband+nBand]
			if channels == 2 && effectiveLowband+nBand <= len(norm2) {
				lowbandY = norm2[effectiveLowband : effectiveLowband+nBand]
			}
		}
		if effectiveLowband >= 0 && lowbandX != nil {
			traceLowband(i, lowbandOffset, effectiveLowband, lowbandX)
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
			if DebugDualStereo {
				fmt.Printf("DualStereo band %d: n=%d, b=%d, B=%d, tell=%d\n",
					i, nBand, b, B, ctx.rd.TellFrac())
				fmt.Printf("  lowbandX nil=%v, lowbandY nil=%v\n", lowbandX == nil, lowbandY == nil)
			}
			xCM = quantBandDecode(&ctx, x, nBand, b/2, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM)
			if DebugDualStereo {
				fmt.Printf("  After L: tell=%d, first 3 coeffs: %.4f %.4f %.4f\n",
					ctx.rd.TellFrac(), x[0], x[1], x[2])
			}
			if channels == 2 {
				yCM = quantBandDecode(&ctx, y, nBand, b/2, B, lowbandY, lm, lowbandOutY, 1.0, lowbandScratch, yCM)
				if DebugDualStereo {
					fmt.Printf("  After R: tell=%d, first 3 coeffs: %.4f %.4f %.4f\n",
						ctx.rd.TellFrac(), y[0], y[1], y[2])
				}
			}
		} else {
			if channels == 2 {
				xCM = quantBandStereoDecode(&ctx, x, y, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, xCM|yCM)
				yCM = xCM
			} else {
				xCM = quantBandDecode(&ctx, x, nBand, b, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM|yCM)
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

// quantAllBandsEncode encodes all frequency bands using PVQ quantization.
// Parameters:
//   - re: range encoder for the main bitstream
//   - channels: number of audio channels (1 or 2)
//   - frameSize: frame size in samples
//   - lm: log2 of M (time resolution multiplier)
//   - start, end: band range to encode
//   - x, y: normalized MDCT coefficients for left/right channels
//   - pulses: bit allocation per band
//   - shortBlocks: number of short blocks (1 for long block)
//   - spread: spreading parameter (0-3)
//   - tapset: window taper selection for comb filter (0-2), tracked for state consistency
//   - dualStereo: whether to use dual stereo mode
//   - intensity: intensity stereo band threshold
//   - tfRes: time-frequency resolution per band
//   - totalBitsQ3: total bit budget in Q3 format
//   - balance: bit balance for allocation
//   - codedBands: number of bands that will be coded
//   - seed: RNG seed for noise filling
//   - complexity: encoder complexity (0-10)
//   - bandE: band energies for stereo decisions
//   - extEnc: extended encoder for high-precision mode (can be nil)
//   - extraBits: extra bits per band for extended mode (can be nil)
//
// Reference: libopus celt/bands.c quant_all_bands()
// quantAllBandsEncodeScratch is the scratch-aware version of quantAllBandsEncode.
func quantAllBandsEncodeScratch(re *rangecoding.Encoder, channels, frameSize, lm int, start, end int,
	x, y []float64, pulses []int, shortBlocks int, spread int, tapset int, dualStereo, intensity int,
	tfRes []int, totalBitsQ3 int, balance int, codedBands int, seed *uint32, complexity int,
	bandE []float64, extEnc *rangecoding.Encoder, extraBits []int, scratch *bandEncodeScratch) (collapse []byte) {
	if re == nil {
		return nil
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	if len(x) < frameSize {
		return nil
	}

	M := 1 << lm
	B := 1
	if shortBlocks > 1 {
		B = shortBlocks
	}

	// Use scratch buffers if available
	if scratch != nil {
		collapse = scratch.ensureCollapse(channels * MaxBands)
		for i := range collapse {
			collapse[i] = 0
		}
	} else {
		collapse = make([]byte, channels*MaxBands)
	}

	normOffset := M * EBands[start]
	normLen := M*EBands[MaxBands-1] - normOffset
	if normLen < 0 {
		normLen = 0
	}

	var norm []float64
	if scratch != nil {
		norm = scratch.ensureNorm(channels * normLen)
		for i := range norm {
			norm[i] = 0
		}
	} else {
		norm = make([]float64, channels*normLen)
	}
	var norm2 []float64
	if channels == 2 {
		norm2 = norm[normLen:]
	}

	maxBand := M * (EBands[end] - EBands[end-1])
	if maxBand < 0 {
		maxBand = 0
	}

	var lowbandScratch []float64
	if scratch != nil {
		lowbandScratch = scratch.ensureLowbandScratch(maxBand)
	} else {
		lowbandScratch = make([]float64, maxBand)
	}

	lowbandOffset := 0
	updateLowband := true
	thetaRDOEnabled := channels == 2 && dualStereo == 0 && complexity >= 8
	ctx := bandCtx{
		re:            re,
		encode:        true,
		extEnc:        nil,
		extraBits:     0,
		bandE:         bandE,
		nbBands:       end,
		channels:      channels,
		spread:        spread,
		remainingBits: 0,
		intensity:     intensity,
		seed:          seed,
		// Resynth must be enabled so lowband folding uses reconstructed data.
		// This matches libopus builds with RESYNTH enabled and improves quality.
		resynth:         true,
		disableInv:      false,
		avoidSplitNoise: B > 1,
		tapset:          tapset,
		encScratch:      scratch,
	}
	if ctx.channels > 0 && ctx.bandE != nil {
		ctx.nbBands = len(ctx.bandE) / ctx.channels
	}

	for i := start; i < end; i++ {
		ctx.band = i
		ctx.extraBits = 0
		if extEnc != nil && extraBits != nil && i < len(extraBits) {
			ctx.extEnc = extEnc
			ctx.extraBits = extraBits[i]
		} else {
			ctx.extEnc = nil
		}
		last := i == end-1
		bandStart := EBands[i] * M
		bandEnd := EBands[i+1] * M
		nBand := bandEnd - bandStart
		if nBand <= 0 {
			continue
		}

		xBand := x[bandStart:bandEnd]
		var yBand []float64
		if channels == 2 && len(y) >= bandEnd {
			yBand = y[bandStart:bandEnd]
		}

		tell := re.TellFrac()
		if i != start {
			balance -= tell
		}
		remaining := totalBitsQ3 - tell - 1
		ctx.remainingBits = remaining

		b := 0
		currBalance := 0
		if i <= codedBands-1 {
			currBalance = celtSudiv(balance, min(3, codedBands-i))
			b = max(0, min(16383, min(remaining+1, pulses[i]+currBalance)))
		}
		if ctx.resynth && (M*EBands[i]-nBand >= M*EBands[start] || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		if i == start+1 {
			specialHybridFolding(norm, norm2, start, M, dualStereo != 0)
		}

		ctx.tfChange = 0
		if tfRes != nil && i < len(tfRes) {
			ctx.tfChange = tfRes[i]
		}

		effectiveLowband := -1
		xCM := 0
		yCM := 0
		if lowbandOffset != 0 && (spread != spreadAggressive || B > 1 || ctx.tfChange < 0) {
			effectiveLowband = max(0, M*EBands[lowbandOffset]-normOffset-nBand)
			foldStart := lowbandOffset
			for {
				foldStart--
				if foldStart <= start {
					foldStart = start
					break
				}
				if M*EBands[foldStart] <= effectiveLowband+normOffset {
					break
				}
			}
			foldEnd := lowbandOffset - 1
			for {
				foldEnd++
				if foldEnd >= i {
					break
				}
				if M*EBands[foldEnd] >= effectiveLowband+normOffset+nBand {
					break
				}
			}
			for fold := foldStart; fold < foldEnd; fold++ {
				xCM |= int(collapse[fold*channels])
				if channels == 2 {
					yCM |= int(collapse[fold*channels+channels-1])
				}
			}
		} else {
			xCM = (1 << B) - 1
			yCM = xCM
		}

		if dualStereo != 0 && i == intensity {
			dualStereo = 0
			if ctx.resynth {
				mergeLimit := M*EBands[i] - normOffset
				if mergeLimit < 0 {
					mergeLimit = 0
				}
				if mergeLimit > len(norm) {
					mergeLimit = len(norm)
				}
				if channels == 2 && mergeLimit > len(norm2) {
					mergeLimit = len(norm2)
				}
				for j := 0; j < mergeLimit; j++ {
					norm[j] = 0.5 * (norm[j] + norm2[j])
				}
			}
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
			xCM = quantBand(&ctx, xBand, nBand, b/2, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM)
			if channels == 2 && yBand != nil {
				yCM = quantBand(&ctx, yBand, nBand, b/2, B, lowbandY, lm, lowbandOutY, 1.0, lowbandScratch, yCM)
			}
		} else {
			if channels == 2 && yBand != nil {
				// Theta RDO: Try both rounding directions and pick the one with lower distortion.
				// Enabled only for high complexity stereo (match libopus theta_rdo).
				// Reference: libopus bands.c quant_all_bands(), theta_rdo logic
				thetaRDO := thetaRDOEnabled && i < intensity
				if thetaRDO {
					// Compute channel weights for distortion measurement
					var leftE, rightE float64
					if bandE != nil && len(bandE) > ctx.nbBands+i {
						leftE = bandE[i]
						rightE = bandE[ctx.nbBands+i]
					}
					w0, w1 := computeChannelWeights(leftE, rightE)

					// Save original input data - use scratch if available
					var xSave, ySave []float64
					if scratch != nil {
						xSave = scratch.ensureXSave(nBand)
						ySave = scratch.ensureYSave(nBand)
					} else {
						xSave = make([]float64, nBand)
						ySave = make([]float64, nBand)
					}
					copy(xSave, xBand)
					copy(ySave, yBand)

					// Save norm data if not last band
					var normSave []float64
					if lowbandOutX != nil {
						if scratch != nil {
							normSave = scratch.ensureNormSave(nBand)
						} else {
							normSave = make([]float64, nBand)
						}
						copy(normSave, lowbandOutX)
					}

					// Save encoder state - use scratch if available
					var ecSave *rangecoding.EncoderState
					if scratch != nil {
						re.SaveStateInto(&scratch.ecSave)
						ecSave = &scratch.ecSave
					} else {
						ecSave = re.SaveState()
					}
					ctxSave := ctx

					// Try encoding with theta_round = -1 (bias toward 0/16384)
					ctx.thetaRound = -1
					cm := xCM | yCM
					xCM0 := quantBandStereo(&ctx, xBand, yBand, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, cm)

					// Compute distortion for first trial
					dist0 := w0*innerProduct(xSave, xBand) + w1*innerProduct(ySave, yBand)

					// Save the result of first trial - use scratch if available
					var xResult0, yResult0 []float64
					if scratch != nil {
						xResult0 = scratch.ensureXResult0(nBand)
						yResult0 = scratch.ensureYResult0(nBand)
					} else {
						xResult0 = make([]float64, nBand)
						yResult0 = make([]float64, nBand)
					}
					copy(xResult0, xBand)
					copy(yResult0, yBand)
					var normResult0 []float64
					if lowbandOutX != nil {
						if scratch != nil {
							normResult0 = scratch.ensureNormResult0(nBand)
						} else {
							normResult0 = make([]float64, nBand)
						}
						copy(normResult0, lowbandOutX)
					}
					var ecSave0 *rangecoding.EncoderState
					if scratch != nil {
						re.SaveStateInto(&scratch.ecSave0)
						ecSave0 = &scratch.ecSave0
					} else {
						ecSave0 = re.SaveState()
					}
					ctxSave0 := ctx
					cm0 := xCM0

					// Restore state for second trial
					re.RestoreState(ecSave)
					ctx = ctxSave
					copy(xBand, xSave)
					copy(yBand, ySave)
					if lowbandOutX != nil && normSave != nil {
						copy(lowbandOutX, normSave)
					}
					// Re-apply special_hybrid_folding if needed
					if i == start+1 {
						specialHybridFolding(norm, norm2, start, M, false)
					}

					// Try encoding with theta_round = +1 (bias toward equal split)
					ctx.thetaRound = 1
					xCM1 := quantBandStereo(&ctx, xBand, yBand, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, cm)

					// Compute distortion for second trial
					dist1 := w0*innerProduct(xSave, xBand) + w1*innerProduct(ySave, yBand)

					// Pick the trial with lower distortion (higher inner product = lower distortion)
					if dist0 >= dist1 {
						// First trial (theta_round = -1) was better
						xCM = cm0
						re.RestoreState(ecSave0)
						ctx = ctxSave0
						copy(xBand, xResult0)
						copy(yBand, yResult0)
						if lowbandOutX != nil && normResult0 != nil {
							copy(lowbandOutX, normResult0)
						}
					} else {
						// Second trial (theta_round = +1) was better
						xCM = xCM1
					}
					yCM = xCM
					ctx.thetaRound = 0 // Reset for subsequent bands
				} else {
					// No theta RDO: use standard encoding
					ctx.thetaRound = 0
					xCM = quantBandStereo(&ctx, xBand, yBand, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, xCM|yCM)
					yCM = xCM
				}
			} else {
				xCM = quantBand(&ctx, xBand, nBand, b, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM|yCM)
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

	return collapse
}
