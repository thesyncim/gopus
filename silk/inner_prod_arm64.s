#include "textflag.h"

// FMADDD operand order in Go Plan 9 ARM64:
//   FMADDD Fm, Fa, Fn, Fd → Fd = Fa + Fn * Fm
// So for sum += x * y: FMADDD Fy, Fsum, Fx, Fsum

// func innerProductF32Asm(a, b []float32, length int) float64
//
// Computes the inner product of two float32 slices, accumulating in float64.
// Uses 4 float64 accumulators to break FMA dependency chains.
// Processes 4 elements per iteration with a scalar tail loop.
//
// Frame layout (ABI0):
//   a_base+0(FP)      *float32
//   a_len+8(FP)       int
//   a_cap+16(FP)      int
//   b_base+24(FP)     *float32
//   b_len+32(FP)      int
//   b_cap+40(FP)      int
//   length+48(FP)     int
//   ret+56(FP)        float64
//
// Register allocation:
//   R0 = a base pointer
//   R1 = b base pointer
//   R2 = remaining count
//   F0 = accumulator 0
//   F1 = accumulator 1
//   F2 = accumulator 2
//   F3 = accumulator 3
//   F4-F7 = loaded float32 values from a (promoted to float64)
//   F8-F11 = loaded float32 values from b (promoted to float64)
TEXT ·innerProductF32Asm(SB), NOSPLIT, $0-64
	MOVD	length+48(FP), R2
	CMP	$1, R2
	BLT	innerProdF32_zero

	MOVD	a_base+0(FP), R0
	MOVD	b_base+24(FP), R1

	FMOVD	ZR, F0	// sum0
	FMOVD	ZR, F1	// sum1
	FMOVD	ZR, F2	// sum2
	FMOVD	ZR, F3	// sum3

	CMP	$4, R2
	BLT	innerProdF32_tail

innerProdF32_loop4:
	// Load 4 float32 values from a[], convert to float64
	FMOVS	(R0), F4
	FCVTSD	F4, F4
	FMOVS	4(R0), F5
	FCVTSD	F5, F5
	FMOVS	8(R0), F6
	FCVTSD	F6, F6
	FMOVS	12(R0), F7
	FCVTSD	F7, F7

	// Load 4 float32 values from b[], convert to float64
	FMOVS	(R1), F8
	FCVTSD	F8, F8
	FMOVS	4(R1), F9
	FCVTSD	F9, F9
	FMOVS	8(R1), F10
	FCVTSD	F10, F10
	FMOVS	12(R1), F11
	FCVTSD	F11, F11

	// FMA: sum_k += a[i+k] * b[i+k], one per accumulator
	FMADDD	F8, F0, F4, F0		// F0 += F4 * F8
	FMADDD	F9, F1, F5, F1		// F1 += F5 * F9
	FMADDD	F10, F2, F6, F2	// F2 += F6 * F10
	FMADDD	F11, F3, F7, F3	// F3 += F7 * F11

	ADD	$16, R0		// a ptr += 4 * sizeof(float32)
	ADD	$16, R1		// b ptr += 4 * sizeof(float32)
	SUB	$4, R2
	CMP	$4, R2
	BGE	innerProdF32_loop4

innerProdF32_tail:
	CBZ	R2, innerProdF32_done

innerProdF32_tail_loop:
	FMOVS	(R0), F4
	FCVTSD	F4, F4
	FMOVS	(R1), F8
	FCVTSD	F8, F8
	FMADDD	F8, F0, F4, F0		// F0 += F4 * F8
	ADD	$4, R0
	ADD	$4, R1
	SUB	$1, R2
	CBNZ	R2, innerProdF32_tail_loop

innerProdF32_done:
	// Combine all 4 accumulators
	FADDD	F1, F0, F0
	FADDD	F2, F0, F0
	FADDD	F3, F0, F0
	FMOVD	F0, ret+56(FP)
	RET

innerProdF32_zero:
	FMOVD	ZR, F0
	FMOVD	F0, ret+56(FP)
	RET

// func energyF32Asm(x []float32, length int) float64
//
// Computes the energy (sum of squares) of a float32 slice, accumulating in float64.
// This is equivalent to innerProductF32Asm(x, x, length) but avoids double-loading.
//
// Frame layout (ABI0):
//   x_base+0(FP)      *float32
//   x_len+8(FP)       int
//   x_cap+16(FP)      int
//   length+24(FP)     int
//   ret+32(FP)        float64
//
// Register allocation:
//   R0 = x base pointer
//   R2 = remaining count
//   F0-F3 = accumulators
//   F4-F7 = loaded float32 values (promoted to float64)
TEXT ·energyF32Asm(SB), NOSPLIT, $0-40
	MOVD	length+24(FP), R2
	CMP	$1, R2
	BLT	energyF32_zero

	MOVD	x_base+0(FP), R0

	FMOVD	ZR, F0
	FMOVD	ZR, F1
	FMOVD	ZR, F2
	FMOVD	ZR, F3

	CMP	$4, R2
	BLT	energyF32_tail

energyF32_loop4:
	FMOVS	(R0), F4
	FCVTSD	F4, F4
	FMOVS	4(R0), F5
	FCVTSD	F5, F5
	FMOVS	8(R0), F6
	FCVTSD	F6, F6
	FMOVS	12(R0), F7
	FCVTSD	F7, F7

	FMADDD	F4, F0, F4, F0		// F0 += F4 * F4
	FMADDD	F5, F1, F5, F1		// F1 += F5 * F5
	FMADDD	F6, F2, F6, F2		// F2 += F6 * F6
	FMADDD	F7, F3, F7, F3		// F3 += F7 * F7

	ADD	$16, R0
	SUB	$4, R2
	CMP	$4, R2
	BGE	energyF32_loop4

energyF32_tail:
	CBZ	R2, energyF32_done

energyF32_tail_loop:
	FMOVS	(R0), F4
	FCVTSD	F4, F4
	FMADDD	F4, F0, F4, F0
	ADD	$4, R0
	SUB	$1, R2
	CBNZ	R2, energyF32_tail_loop

energyF32_done:
	FADDD	F1, F0, F0
	FADDD	F2, F0, F0
	FADDD	F3, F0, F0
	FMOVD	F0, ret+32(FP)
	RET

energyF32_zero:
	FMOVD	ZR, F0
	FMOVD	F0, ret+32(FP)
	RET
