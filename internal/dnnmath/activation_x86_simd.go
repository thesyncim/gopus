//go:build amd64 && goexperiment.simd && !nosimd

package dnnmath

import "simd/archsimd"

var dnnX86Enabled = archsimd.X86.AVX2() && archsimd.X86.FMA()

// Cgemv8x4QuantizeInputX86 matches dnn/vec_avx.h:vector_ps_to_epi8 in
// libopus's AVX2/FMA DRED build. The unsigned byte encodes the +127 bias.
func Cgemv8x4QuantizeInputX86(x float32) uint8 {
	if !dnnX86Enabled {
		return uint8(int32(Cgemv8x4QuantizeInputScalar(x)) + 127)
	}
	v := archsimd.BroadcastFloat32x4(x).MulAdd(
		archsimd.BroadcastFloat32x4(127), archsimd.BroadcastFloat32x4(127))
	// CVTPS2DQ then PACKUSDW/PACKUSWB: the second pack treats uint16
	// values >=32768 as negative signed16 and saturates them back to zero.
	q := v.Round().ConvertToInt32().GetElem(0)
	if q < 0 || q >= 32768 {
		return 0
	}
	if q > 255 {
		return 255
	}
	return uint8(q)
}

// The selected x86 DRED archive uses dnn/vec_avx.h's AVX2/FMA Padé
// polynomial and VRCPPS estimate for both complete vectors and the tail.
func sigmoidVectorX86(out, in []float32, n int) {
	if !dnnX86Enabled {
		SigmoidVectorScalarApprox(out, in, n)
		return
	}
	i := 0
	for ; i+8 <= n; i += 8 {
		sigmoid8X86(archsimd.LoadFloat32x8(in[i:])).Store(out[i:])
	}
	for ; i < n; i++ {
		out[i] = sigmoid8X86(archsimd.BroadcastFloat32x8(in[i])).GetLo().GetElem(0)
	}
}

func tanhVectorX86(out, in []float32, n int) {
	if !dnnX86Enabled {
		TanhVectorScalarApprox(out, in, n)
		return
	}
	i := 0
	for ; i+8 <= n; i += 8 {
		tanh8X86(archsimd.LoadFloat32x8(in[i:])).Store(out[i:])
	}
	for ; i < n; i++ {
		out[i] = tanhApproxX86(in[i])
	}
}

func tanhApproxX86(x float32) float32 {
	if !dnnX86Enabled {
		return TanhScalarApprox(x)
	}
	return tanh8X86(archsimd.BroadcastFloat32x8(x)).GetLo().GetElem(0)
}

func expVectorX86(out, in []float32, n int) {
	if !dnnX86Enabled {
		ExpVectorScalarApprox(out, in, n)
		return
	}
	i := 0
	for ; i+8 <= n; i += 8 {
		exp8X86(archsimd.LoadFloat32x8(in[i:])).Store(out[i:])
	}
	for ; i < n; i++ {
		out[i] = exp8X86(archsimd.BroadcastFloat32x8(in[i])).GetLo().GetElem(0)
	}
}

func exp8X86(x archsimd.Float32x8) archsimd.Float32x8 {
	// dnn/vec_avx.h:exp8_approx scales and clamps before splitting the
	// exponent bits from the FMA-evaluated cubic mantissa.
	scaled := x.Mul(archsimd.BroadcastFloat32x8(1.44269504))
	scaled = archsimd.BroadcastFloat32x8(-50).Max(archsimd.BroadcastFloat32x8(50).Min(scaled))
	integerFloat := scaled.Floor()
	integer := integerFloat.ConvertToInt32()
	frac := scaled.Sub(integerFloat)
	mantissa := archsimd.BroadcastFloat32x8(0.078024523).
		MulAdd(frac, archsimd.BroadcastFloat32x8(0.22606716)).
		MulAdd(frac, archsimd.BroadcastFloat32x8(0.69583354)).
		MulAdd(frac, archsimd.BroadcastFloat32x8(0.99992522))
	bits := integer.ShiftLeft(archsimd.BroadcastUint32x8(23)).Add(mantissa.AsInt32x8())
	return bits.AsFloat32x8()
}

func sigmoid8X86(x archsimd.Float32x8) archsimd.Float32x8 {
	x2 := x.Mul(x)
	num := archsimd.BroadcastFloat32x8(0.00950985).MulAdd(x2, archsimd.BroadcastFloat32x8(6.02452230)).MulAdd(x2, archsimd.BroadcastFloat32x8(238.13200378))
	den := archsimd.BroadcastFloat32x8(0.74287558).MulAdd(x2, archsimd.BroadcastFloat32x8(103.34200287)).MulAdd(x2, archsimd.BroadcastFloat32x8(952.72399902))
	y := num.Mul(x).MulAdd(den.Reciprocal(), archsimd.BroadcastFloat32x8(0.5))
	return archsimd.BroadcastFloat32x8(0).Max(archsimd.BroadcastFloat32x8(1).Min(y))
}

func tanh8X86(x archsimd.Float32x8) archsimd.Float32x8 {
	x2 := x.Mul(x)
	num := archsimd.BroadcastFloat32x8(0.60863042).MulAdd(x2, archsimd.BroadcastFloat32x8(96.39235687)).MulAdd(x2, archsimd.BroadcastFloat32x8(952.52801514))
	den := archsimd.BroadcastFloat32x8(11.88600922).MulAdd(x2, archsimd.BroadcastFloat32x8(413.36801147)).MulAdd(x2, archsimd.BroadcastFloat32x8(952.72399902))
	y := num.Mul(x).Mul(den.Reciprocal())
	return archsimd.BroadcastFloat32x8(-1).Max(archsimd.BroadcastFloat32x8(1).Min(y))
}
