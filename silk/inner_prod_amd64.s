#include "textflag.h"

// func innerProductF32Asm(a, b []float32, length int) float64
//
// Computes the inner product of two float32 slices, accumulating in float64.
// Uses 4 float64 accumulators with FMA to break dependency chains.
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
//   SI = a base pointer
//   DI = b base pointer
//   CX = remaining count
//   X0-X3 = float64 accumulators
//   X4-X7 = a values (float32→float64)
//   X8-X11 = b values (float32→float64)
TEXT ·innerProductF32Asm(SB), NOSPLIT, $0-64
	MOVQ	length+48(FP), CX
	TESTQ	CX, CX
	JLE	innerProdF32_zero

	MOVQ	a_base+0(FP), SI
	MOVQ	b_base+24(FP), DI

	XORPD	X0, X0		// sum0
	XORPD	X1, X1		// sum1
	XORPD	X2, X2		// sum2
	XORPD	X3, X3		// sum3

	CMPQ	CX, $4
	JLT	innerProdF32_tail

innerProdF32_loop4:
	// Load 4 float32 from a, convert to float64
	MOVSS	(SI), X4
	CVTSS2SD X4, X4
	MOVSS	4(SI), X5
	CVTSS2SD X5, X5
	MOVSS	8(SI), X6
	CVTSS2SD X6, X6
	MOVSS	12(SI), X7
	CVTSS2SD X7, X7

	// Load 4 float32 from b, convert to float64
	MOVSS	(DI), X8
	CVTSS2SD X8, X8
	MOVSS	4(DI), X9
	CVTSS2SD X9, X9
	MOVSS	8(DI), X10
	CVTSS2SD X10, X10
	MOVSS	12(DI), X11
	CVTSS2SD X11, X11

	// FMA: sum_k += a[i+k] * b[i+k]
	VFMADD231SD X8, X4, X0
	VFMADD231SD X9, X5, X1
	VFMADD231SD X10, X6, X2
	VFMADD231SD X11, X7, X3

	ADDQ	$16, SI
	ADDQ	$16, DI
	SUBQ	$4, CX
	CMPQ	CX, $4
	JGE	innerProdF32_loop4

innerProdF32_tail:
	TESTQ	CX, CX
	JZ	innerProdF32_done

innerProdF32_tail_loop:
	MOVSS	(SI), X4
	CVTSS2SD X4, X4
	MOVSS	(DI), X8
	CVTSS2SD X8, X8
	VFMADD231SD X8, X4, X0
	ADDQ	$4, SI
	ADDQ	$4, DI
	DECQ	CX
	JNZ	innerProdF32_tail_loop

innerProdF32_done:
	// Combine 4 accumulators
	ADDSD	X1, X0
	ADDSD	X2, X0
	ADDSD	X3, X0
	MOVSD	X0, ret+56(FP)
	RET

innerProdF32_zero:
	XORPD	X0, X0
	MOVSD	X0, ret+56(FP)
	RET

// func energyF32Asm(x []float32, length int) float64
//
// Computes the energy (sum of squares) of a float32 slice, accumulating in float64.
// Equivalent to innerProductF32Asm(x, x, length) but avoids double-loading.
//
// Frame layout (ABI0):
//   x_base+0(FP)      *float32
//   x_len+8(FP)       int
//   x_cap+16(FP)      int
//   length+24(FP)     int
//   ret+32(FP)        float64
//
// Register allocation:
//   SI = x base pointer
//   CX = remaining count
//   X0-X3 = float64 accumulators
//   X4-X7 = x values (float32→float64)
TEXT ·energyF32Asm(SB), NOSPLIT, $0-40
	MOVQ	length+24(FP), CX
	TESTQ	CX, CX
	JLE	energyF32_zero

	MOVQ	x_base+0(FP), SI

	XORPD	X0, X0
	XORPD	X1, X1
	XORPD	X2, X2
	XORPD	X3, X3

	CMPQ	CX, $4
	JLT	energyF32_tail

energyF32_loop4:
	MOVSS	(SI), X4
	CVTSS2SD X4, X4
	MOVSS	4(SI), X5
	CVTSS2SD X5, X5
	MOVSS	8(SI), X6
	CVTSS2SD X6, X6
	MOVSS	12(SI), X7
	CVTSS2SD X7, X7

	// FMA: sum_k += x[i+k]^2
	VFMADD231SD X4, X4, X0
	VFMADD231SD X5, X5, X1
	VFMADD231SD X6, X6, X2
	VFMADD231SD X7, X7, X3

	ADDQ	$16, SI
	SUBQ	$4, CX
	CMPQ	CX, $4
	JGE	energyF32_loop4

energyF32_tail:
	TESTQ	CX, CX
	JZ	energyF32_done

energyF32_tail_loop:
	MOVSS	(SI), X4
	CVTSS2SD X4, X4
	VFMADD231SD X4, X4, X0
	ADDQ	$4, SI
	DECQ	CX
	JNZ	energyF32_tail_loop

energyF32_done:
	ADDSD	X1, X0
	ADDSD	X2, X0
	ADDSD	X3, X0
	MOVSD	X0, ret+32(FP)
	RET

energyF32_zero:
	XORPD	X0, X0
	MOVSD	X0, ret+32(FP)
	RET
