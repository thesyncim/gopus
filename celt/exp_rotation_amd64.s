#include "textflag.h"

// func expRotation1Stride2(x []float64, length int, c, s float64)
//
// Applies forward and backward Givens rotations for the stride-2 case of
// expRotation1. Uses AVX packed float64 for the 2-wide unrolled core
// loops and scalar for tail elements.
//
// FMA contraction order matches Go compiler output:
//   c*x2 + s*x1  →  temp = s*x1 (MUL), result = fma(c, x2, temp)
//   c*x1 + ms*x2 →  temp = ms*x2 (MUL), result = fma(c, x1, temp)
//
// Register allocation:
//   R8    = x base pointer
//   CX    = length
//   DX    = loop index i
//   R9    = loop limit
//   R10   = address temporary
//   X0    = {c, c}
//   X1    = {s, s}
//   X2    = {ms, ms}
//   X3-X7 = temporaries
TEXT ·expRotation1Stride2(SB), NOSPLIT, $0-48
	MOVQ x_base+0(FP), R8
	MOVQ length+24(FP), CX

	// Broadcast c, s, ms
	VMOVSD    c+32(FP), X0
	VUNPCKLPD X0, X0, X0         // X0 = {c, c}
	VMOVSD    s+40(FP), X1
	VUNPCKLPD X1, X1, X1         // X1 = {s, s}
	VXORPD    X2, X2, X2
	VSUBPD    X1, X2, X2         // X2 = {-s, -s}

	// ===== FORWARD PASS =====
	XORQ  DX, DX                 // i = 0
	MOVQ  CX, R9
	SUBQ  $3, R9                 // R9 = length - 3
	CMPQ  R9, $1
	JLT   fwd_scalar_init

fwd_simd:
	LEAQ  (R8)(DX*8), R10

	VMOVUPD (R10), X3            // {x[i], x[i+1]}  (pair1)
	VMOVUPD 16(R10), X4          // {x[i+2], x[i+3]} (pair2)

	// c*pair2 + s*pair1: temp = s*pair1, result = fma(c, pair2, temp)
	VMULPD        X1, X3, X5     // X5 = s * pair1
	VFMADD231PD   X0, X4, X5     // X5 += c * pair2

	// c*pair1 + ms*pair2: temp = ms*pair2, result = fma(c, pair1, temp)
	VMULPD        X2, X4, X6     // X6 = ms * pair2
	VFMADD231PD   X0, X3, X6     // X6 += c * pair1

	VMOVUPD X5, 16(R10)
	VMOVUPD X6, (R10)

	ADDQ  $2, DX
	CMPQ  DX, R9
	JLT   fwd_simd

fwd_scalar_init:
	MOVQ  CX, R9
	SUBQ  $2, R9                 // R9 = end = length - 2

fwd_scalar:
	CMPQ  DX, R9
	JGE   bwd_init

	LEAQ  (R8)(DX*8), R10
	VMOVSD (R10), X3             // x[i]
	VMOVSD 16(R10), X4           // x[i+2]

	// c*x[i+2] + s*x[i]
	VMULSD      X1, X3, X5       // X5 = s * x[i]
	VFMADD231SD X0, X4, X5       // X5 += c * x[i+2]
	VMOVSD X5, 16(R10)

	// c*x[i] + ms*x[i+2]
	VMULSD      X2, X4, X6       // X6 = ms * x[i+2]
	VFMADD231SD X0, X3, X6       // X6 += c * x[i]
	VMOVSD X6, (R10)

	INCQ  DX
	JMP   fwd_scalar

bwd_init:
	// ===== BACKWARD PASS =====
	MOVQ  CX, DX
	SUBQ  $5, DX                 // i = length - 5
	CMPQ  DX, $1
	JLT   bwd_scalar_init

bwd_simd:
	LEAQ  (R8)(DX*8), R10
	SUBQ  $8, R10                // R10 = &x[i-1]

	VMOVUPD (R10), X3            // {x[i-1], x[i]}    (lo)
	VMOVUPD 16(R10), X4          // {x[i+1], x[i+2]}  (hi)

	// c*hi + s*lo: temp = s*lo, result = fma(c, hi, temp)
	VMULPD        X1, X3, X5
	VFMADD231PD   X0, X4, X5

	// c*lo + ms*hi: temp = ms*hi, result = fma(c, lo, temp)
	VMULPD        X2, X4, X6
	VFMADD231PD   X0, X3, X6

	VMOVUPD X5, 16(R10)
	VMOVUPD X6, (R10)

	SUBQ  $2, DX
	CMPQ  DX, $1
	JGE   bwd_simd

bwd_scalar_init:

bwd_scalar:
	TESTQ DX, DX
	JS    done

	LEAQ  (R8)(DX*8), R10
	VMOVSD (R10), X3             // x[i]
	VMOVSD 16(R10), X4           // x[i+2]

	VMULSD      X1, X3, X5
	VFMADD231SD X0, X4, X5
	VMOVSD X5, 16(R10)

	VMULSD      X2, X4, X6
	VFMADD231SD X0, X3, X6
	VMOVSD X6, (R10)

	DECQ  DX
	JMP   bwd_scalar

done:
	VZEROUPPER
	RET
