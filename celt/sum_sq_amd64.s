//go:build amd64 && gopus_sum_sq_asm

#include "textflag.h"

// func sumOfSquaresF64toF32(x []float64, n int) float64
//
// Converts float64 elements to float32 and accumulates the sum of squares
// in float32 precision (matching the Go fallback exactly).
TEXT ·sumOfSquaresF64toF32(SB), NOSPLIT, $0-40
	MOVQ x_base+0(FP), AX
	MOVQ n+24(FP), CX

	// Scalar float32 accumulator in X8[0].
	VXORPS X8, X8, X8

	TESTQ CX, CX
	JLE   sq_done

	CMPQ CX, $2
	JLT  sq_tail

sq_loop2:
	VMOVSD (AX), X0
	VCVTSD2SS X0, X0, X0
	VMULSS X0, X0, X0
	VADDSS X0, X8, X8

	VMOVSD 8(AX), X1
	VCVTSD2SS X1, X1, X1
	VMULSS X1, X1, X1
	VADDSS X1, X8, X8

	ADDQ $16, AX
	SUBQ $2, CX
	CMPQ CX, $2
	JGE  sq_loop2

sq_tail:
	TESTQ CX, CX
	JLE   sq_done

	VMOVSD (AX), X0
	VCVTSD2SS X0, X0, X0
	VMULSS X0, X0, X0
	VADDSS X0, X8, X8

sq_done:
	VCVTSS2SD X8, X8, X8
	VMOVSD X8, ret+32(FP)
	VZEROUPPER
	RET
