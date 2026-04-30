#include "textflag.h"

// WORD-encoded instructions not supported by Go assembler:
//   DUP  Vd.2D, Vn.D[0]       = 0x4E080400 | (Vn<<5) | Vd
//   FMUL Vd.2D, Vn.2D, Vm.2D  = 0x6E60DC00 | (Vm<<16) | (Vn<<5) | Vd

// func expRotation1Stride2(x []float64, length int, c, s float64)
//
// Applies forward and backward Givens rotations for the stride-2 case of
// expRotation1. Uses NEON float64x2 for the 2-wide unrolled core loops
// and scalar FMULD/FMADDD for tail elements.
//
// FMA contraction order matches Go compiler output:
//   c*x2 + s*x1  →  temp = s*x1 (FMUL), result = fma(c, x2, temp)
//   c*x1 + ms*x2 →  temp = ms*x2 (FMUL), result = fma(c, x1, temp)
//
// Register allocation:
//   R0    = x base pointer
//   R1    = length
//   R2    = loop index i
//   R3    = loop limit
//   R4,R5 = address temporaries
//   V0.D2 = {c, c}    F0 = c (scalar)
//   V1.D2 = {s, s}    F1 = s (scalar)
//   V2.D2 = {ms, ms}  F2 = ms (scalar)
//   V3-V6 = temporaries
TEXT ·expRotation1Stride2(SB), NOSPLIT, $0-48
	MOVD  x_base+0(FP), R0
	MOVD  length+24(FP), R1
	FMOVD c+32(FP), F0
	FMOVD s+40(FP), F1
	FNEGD F1, F2                  // F2 = ms = -s

	// Broadcast c, s, ms to .2D vectors
	WORD $0x4E080400              // DUP V0.2D, V0.D[0]
	WORD $0x4E080421              // DUP V1.2D, V1.D[0]
	WORD $0x4E080442              // DUP V2.2D, V2.D[0]

	// ===== FORWARD PASS =====
	// SIMD loop: i from 0 while i < length-3, step 2
	MOVD  ZR, R2
	SUB   $3, R1, R3
	CMP   $1, R3
	BLT   fwd_scalar_init

fwd_simd:
	LSL   $3, R2, R4
	ADD   R0, R4, R4              // R4 = &x[i]
	ADD   $16, R4, R5             // R5 = &x[i+2]

	VLD1  (R4), [V3.D2]          // V3 = {x[i], x[i+1]}  (pair1)
	VLD1  (R5), [V4.D2]          // V4 = {x[i+2], x[i+3]} (pair2)

	// c*pair2 + s*pair1: temp = s*pair1 (FMUL), result = fma(c, pair2, temp)
	WORD  $0x6E63DC25             // FMUL V5.2D, V1.2D, V3.2D (V5 = s*pair1)
	VFMLA V0.D2, V4.D2, V5.D2   // V5 += c*pair2

	// c*pair1 + ms*pair2: temp = ms*pair2 (FMUL), result = fma(c, pair1, temp)
	WORD  $0x6E64DC46             // FMUL V6.2D, V2.2D, V4.2D (V6 = ms*pair2)
	VFMLA V0.D2, V3.D2, V6.D2   // V6 += c*pair1

	VST1  [V5.D2], (R5)
	VST1  [V6.D2], (R4)

	ADD   $2, R2
	CMP   R3, R2
	BLT   fwd_simd

fwd_scalar_init:
	SUB   $2, R1, R3              // R3 = end = length - 2

fwd_scalar:
	CMP   R3, R2
	BGE   bwd_init

	LSL   $3, R2, R4
	ADD   R0, R4, R4
	FMOVD (R4), F3                // x[i]
	FMOVD 16(R4), F4             // x[i+2]

	// c*x[i+2] + s*x[i]: temp = s*x[i], result = fma(c, x[i+2], temp)
	FMULD  F1, F3, F5            // F5 = s * x[i]
	FMADDD F4, F5, F0, F5        // F5 = F5 + F0 * F4 = s*x[i] + c*x[i+2]
	FMOVD  F5, 16(R4)

	// c*x[i] + ms*x[i+2]: temp = ms*x[i+2], result = fma(c, x[i], temp)
	FMULD  F2, F4, F6            // F6 = ms * x[i+2]
	FMADDD F3, F6, F0, F6        // F6 = F6 + F0 * F3 = ms*x[i+2] + c*x[i]
	FMOVD  F6, (R4)

	ADD   $1, R2
	B     fwd_scalar

bwd_init:
	// ===== BACKWARD PASS =====
	// SIMD loop: i from length-5 while i >= 1, step -2
	SUB   $5, R1, R2
	CMP   $1, R2
	BLT   bwd_scalar_init

bwd_simd:
	SUB   $1, R2, R4
	LSL   $3, R4, R4
	ADD   R0, R4, R4              // R4 = &x[i-1]
	ADD   $16, R4, R5             // R5 = &x[i+1]

	VLD1  (R4), [V3.D2]          // V3 = {x[i-1], x[i]}  (lo)
	VLD1  (R5), [V4.D2]          // V4 = {x[i+1], x[i+2]} (hi)

	// c*hi + s*lo: temp = s*lo, result = fma(c, hi, temp)
	WORD  $0x6E63DC25             // FMUL V5.2D, V1.2D, V3.2D (V5 = s*lo)
	VFMLA V0.D2, V4.D2, V5.D2   // V5 += c*hi

	// c*lo + ms*hi: temp = ms*hi, result = fma(c, lo, temp)
	WORD  $0x6E64DC46             // FMUL V6.2D, V2.2D, V4.2D (V6 = ms*hi)
	VFMLA V0.D2, V3.D2, V6.D2   // V6 += c*lo

	VST1  [V5.D2], (R5)
	VST1  [V6.D2], (R4)

	SUB   $2, R2
	CMP   $1, R2
	BGE   bwd_simd

bwd_scalar_init:

bwd_scalar:
	CMP   $0, R2
	BLT   done

	LSL   $3, R2, R4
	ADD   R0, R4, R4
	FMOVD (R4), F3
	FMOVD 16(R4), F4

	// c*x[i+2] + s*x[i]
	FMULD  F1, F3, F5
	FMADDD F4, F5, F0, F5
	FMOVD  F5, 16(R4)

	// c*x[i] + ms*x[i+2]
	FMULD  F2, F4, F6
	FMADDD F3, F6, F0, F6
	FMOVD  F6, (R4)

	SUB   $1, R2
	B     bwd_scalar

done:
	RET

// func expRotation1Stride1(x []float64, length int, c, s float64)
//
// Scalar arm64 implementation for the stride-1 expRotation1 path. The
// recurrence is dependency-heavy, so the main win over Go is removing per-step
// bounds checks while preserving the compiler's FMA contraction order.
TEXT ·expRotation1Stride1(SB), NOSPLIT, $0-48
	MOVD  x_base+0(FP), R0
	MOVD  length+24(FP), R1
	FMOVD c+32(FP), F0
	FMOVD s+40(FP), F1
	FNEGD F1, F2                  // F2 = ms = -s

	CMP   $2, R1
	BLT   stride1_done

	// ===== FORWARD PASS =====
	// i from 0 while i+1 < length-1, step 2.
	MOVD  ZR, R2
	SUB   $2, R1, R3

stride1_fwd_unroll:
	CMP   R3, R2
	BGE   stride1_fwd_scalar_init

	LSL   $3, R2, R4
	ADD   R0, R4, R4              // R4 = &x[i]
	FMOVD (R4), F3                // x[i]
	FMOVD 8(R4), F4               // x[i+1]

	// x[i+1] = c*x[i+1] + s*x[i]
	FMULD  F1, F3, F5
	FMADDD F4, F5, F0, F5

	// x[i] = c*x[i] + ms*x[i+1]
	FMULD  F2, F4, F6
	FMADDD F3, F6, F0, F6
	FMOVD  F6, (R4)

	// Second dependent step uses the newly-written x[i+1].
	FMOVD 16(R4), F7              // x[i+2]
	FMULD  F1, F5, F8
	FMADDD F7, F8, F0, F8
	FMOVD  F8, 16(R4)

	FMULD  F2, F7, F9
	FMADDD F5, F9, F0, F9
	FMOVD  F9, 8(R4)

	ADD   $2, R2
	B     stride1_fwd_unroll

stride1_fwd_scalar_init:
	SUB   $1, R1, R3

stride1_fwd_scalar:
	CMP   R3, R2
	BGE   stride1_bwd_init

	LSL   $3, R2, R4
	ADD   R0, R4, R4              // R4 = &x[i]
	FMOVD (R4), F3                // x[i]
	FMOVD 8(R4), F4               // x[i+1]

	FMULD  F1, F3, F5
	FMADDD F4, F5, F0, F5
	FMOVD  F5, 8(R4)

	FMULD  F2, F4, F6
	FMADDD F3, F6, F0, F6
	FMOVD  F6, (R4)

	ADD   $1, R2
	B     stride1_fwd_scalar

stride1_bwd_init:
	// ===== BACKWARD PASS =====
	// i from length-3 down to 1, step -2.
	SUB   $3, R1, R2

stride1_bwd_unroll:
	CMP   $0, R2
	BLE   stride1_bwd_scalar

	LSL   $3, R2, R4
	ADD   R0, R4, R4              // R4 = &x[i]
	FMOVD (R4), F3                // x[i]
	FMOVD 8(R4), F4               // x[i+1]

	// x[i+1] = c*x[i+1] + s*x[i]
	FMULD  F1, F3, F5
	FMADDD F4, F5, F0, F5
	FMOVD  F5, 8(R4)

	// x[i] = c*x[i] + ms*x[i+1]
	FMULD  F2, F4, F6
	FMADDD F3, F6, F0, F6

	// Second dependent step updates x[i-1], x[i] using the new x[i].
	FMOVD -8(R4), F7              // x[i-1]
	FMULD  F1, F7, F8
	FMADDD F6, F8, F0, F8
	FMOVD  F8, (R4)

	FMULD  F2, F6, F9
	FMADDD F7, F9, F0, F9
	FMOVD  F9, -8(R4)

	SUB   $2, R2
	B     stride1_bwd_unroll

stride1_bwd_scalar:
	CMP   $0, R2
	BLT   stride1_done

	LSL   $3, R2, R4
	ADD   R0, R4, R4              // R4 = &x[i]
	FMOVD (R4), F3                // x[i]
	FMOVD 8(R4), F4               // x[i+1]

	FMULD  F1, F3, F5
	FMADDD F4, F5, F0, F5
	FMOVD  F5, 8(R4)

	FMULD  F2, F4, F6
	FMADDD F3, F6, F0, F6
	FMOVD  F6, (R4)

	SUB   $1, R2
	B     stride1_bwd_scalar

stride1_done:
	RET
