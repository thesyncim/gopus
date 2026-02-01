//go:build amd64 && !purego

#include "textflag.h"

// func imdctPreRotateF32AVX(out []float32, spectrum []float64, trig []float32, n2, n4 int)
TEXT ·imdctPreRotateF32AVX(SB), NOSPLIT, $0-88
	MOVQ	out_base+0(FP), DI
	MOVQ	spectrum_base+24(FP), SI
	MOVQ	trig_base+48(FP), DX
	MOVQ	n2+72(FP), CX
	MOVQ	n4+80(FP), R8

	TESTQ	R8, R8
	JLE	imdct_prerotate_done

	XORQ	R9, R9

imdct_prerotate_loop:
	MOVQ	R9, R10
	SHLQ	$4, R10
	ADDQ	SI, R10
	MOVSD	(R10), X0
	CVTSD2SS	X0, X0

	MOVQ	CX, R11
	DECQ	R11
	MOVQ	R9, R12
	SHLQ	$1, R12
	SUBQ	R12, R11
	LEAQ	(SI)(R11*8), R13
	MOVSD	(R13), X1
	CVTSD2SS	X1, X1

	MOVSS	(DX)(R9*4), X2
	MOVQ	R8, R14
	ADDQ	R9, R14
	MOVSS	(DX)(R14*4), X3

	VMULSS	X1, X2, X4
	VMULSS	X0, X3, X5
	VADDSS	X4, X5, X4

	VMULSS	X0, X2, X6
	VMULSS	X1, X3, X7
	VSUBSS	X7, X6, X6

	MOVSS	X6, (DI)(R9*8)
	MOVSS	X4, 4(DI)(R9*8)

	INCQ	R9
	CMPQ	R9, R8
	JL	imdct_prerotate_loop

imdct_prerotate_done:
	VZEROUPPER
	RET

// func imdctPreRotateF32AVX2(out []float32, spectrum []float64, trig []float32, n2, n4 int)
TEXT ·imdctPreRotateF32AVX2(SB), NOSPLIT, $0-88
	JMP	·imdctPreRotateF32AVX(SB)

// func imdctPostRotateF32AVX(buf []float32, trig []float32, n2, n4 int)
TEXT ·imdctPostRotateF32AVX(SB), NOSPLIT, $0-64
	MOVQ	buf_base+0(FP), DI
	MOVQ	trig_base+24(FP), SI
	MOVQ	n2+48(FP), CX
	MOVQ	n4+56(FP), DX

	MOVQ	DX, R8
	INCQ	R8
	SHRQ	$1, R8
	TESTQ	R8, R8
	JLE	imdct_postrotate_done

	XORQ	R9, R9

imdct_postrotate_loop:
	MOVQ	R9, R10
	SHLQ	$1, R10
	MOVQ	CX, R11
	SUBQ	$2, R11
	SUBQ	R10, R11

	MOVSS	(DI)(R10*4), X0
	MOVSS	4(DI)(R10*4), X1

	MOVSS	(SI)(R9*4), X2
	LEAQ	(SI)(DX*4), R12
	MOVSS	(R12)(R9*4), X3

	VMULSS	X1, X2, X4
	VMULSS	X0, X3, X5
	VADDSS	X4, X5, X4

	VMULSS	X1, X3, X6
	VMULSS	X0, X2, X7
	VSUBSS	X7, X6, X6

	MOVSS	(DI)(R11*4), X8
	MOVSS	4(DI)(R11*4), X9

	MOVSS	X4, (DI)(R10*4)
	MOVSS	X6, 4(DI)(R11*4)

	MOVQ	DX, R13
	SUBQ	R9, R13
	DECQ	R13
	MOVQ	CX, R14
	SUBQ	R9, R14
	DECQ	R14

	MOVSS	(SI)(R13*4), X2
	MOVSS	(SI)(R14*4), X3

	VMULSS	X9, X2, X4
	VMULSS	X8, X3, X5
	VADDSS	X4, X5, X4

	VMULSS	X9, X3, X6
	VMULSS	X8, X2, X7
	VSUBSS	X7, X6, X6

	MOVSS	X4, (DI)(R11*4)
	MOVSS	X6, 4(DI)(R10*4)

	INCQ	R9
	CMPQ	R9, R8
	JL	imdct_postrotate_loop

imdct_postrotate_done:
	VZEROUPPER
	RET

// func imdctPostRotateF32AVX2(buf []float32, trig []float32, n2, n4 int)
TEXT ·imdctPostRotateF32AVX2(SB), NOSPLIT, $0-64
	JMP	·imdctPostRotateF32AVX(SB)
