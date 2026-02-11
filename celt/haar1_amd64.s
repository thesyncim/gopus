#include "textflag.h"

// float32 1/sqrt(2) = 0x3F3504F3
DATA invSqrt2F32amd<>+0(SB)/4, $0x3F3504F3
GLOBL invSqrt2F32amd<>(SB), RODATA|NOPTR, $4

// func haar1Stride1Asm(x []float64, n0 int)
//
// Applies the Haar butterfly to n0 consecutive pairs of float64 values:
//   tmp1 = invSqrt2_f32 * float32(x[2*j])
//   tmp2 = invSqrt2_f32 * float32(x[2*j+1])
//   x[2*j]   = float64(tmp1 + tmp2)
//   x[2*j+1] = float64(tmp1 - tmp2)
//
// Frame layout (ABI0):
//   x_base+0(FP)   *float64
//   x_len+8(FP)    int
//   x_cap+16(FP)   int
//   n0+24(FP)      int
//
// Register allocation:
//   SI = x pointer (advances by 32 per 2 pairs)
//   CX = remaining pair count
//   X15 = invSqrt2 constant (float32, scalar)
//   X0-X7 = scratch
TEXT ·haar1Stride1Asm(SB), NOSPLIT, $0-32
	MOVQ	n0+24(FP), CX
	TESTQ	CX, CX
	JLE	haar1s1_done

	MOVQ	x_base+0(FP), SI
	MOVSS	invSqrt2F32amd<>(SB), X15

	CMPQ	CX, $2
	JLT	haar1s1_tail

haar1s1_loop2:
	// --- Pair 0 ---
	MOVSD	(SI), X0		// x[2j] as float64
	MOVSD	8(SI), X1		// x[2j+1] as float64
	CVTSD2SS X0, X2		// narrow to float32
	CVTSD2SS X1, X3		// narrow to float32
	MULSS	X15, X2			// tmp1 = invSqrt2 * float32(x[2j])
	MULSS	X15, X3			// tmp2 = invSqrt2 * float32(x[2j+1])
	MOVSS	X2, X4			// copy tmp1
	ADDSS	X3, X4			// tmp1 + tmp2
	SUBSS	X3, X2			// tmp1 - tmp2
	CVTSS2SD X4, X0		// widen to float64
	CVTSS2SD X2, X1		// widen to float64
	MOVSD	X0, (SI)
	MOVSD	X1, 8(SI)

	// --- Pair 1 ---
	MOVSD	16(SI), X0
	MOVSD	24(SI), X1
	CVTSD2SS X0, X2
	CVTSD2SS X1, X3
	MULSS	X15, X2
	MULSS	X15, X3
	MOVSS	X2, X4
	ADDSS	X3, X4
	SUBSS	X3, X2
	CVTSS2SD X4, X0
	CVTSS2SD X2, X1
	MOVSD	X0, 16(SI)
	MOVSD	X1, 24(SI)

	ADDQ	$32, SI			// advance by 4 float64 = 32 bytes
	SUBQ	$2, CX
	CMPQ	CX, $2
	JGE	haar1s1_loop2

haar1s1_tail:
	TESTQ	CX, CX
	JZ	haar1s1_done

	MOVSD	(SI), X0
	MOVSD	8(SI), X1
	CVTSD2SS X0, X2
	CVTSD2SS X1, X3
	MULSS	X15, X2
	MULSS	X15, X3
	MOVSS	X2, X4
	ADDSS	X3, X4
	SUBSS	X3, X2
	CVTSS2SD X4, X0
	CVTSS2SD X2, X1
	MOVSD	X0, (SI)
	MOVSD	X1, 8(SI)

haar1s1_done:
	RET
