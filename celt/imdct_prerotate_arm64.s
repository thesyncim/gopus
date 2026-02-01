//go:build arm64 && !purego

#include "textflag.h"

// func imdctPreRotateF32Asm(out []float32, spectrum []float64, trig []float32, n2, n4 int)
TEXT ·imdctPreRotateF32Asm(SB), NOSPLIT, $0-88
	MOVD	out_base+0(FP), R0
	MOVD	spectrum_base+24(FP), R1
	MOVD	trig_base+48(FP), R2
	MOVD	n2+72(FP), R3
	MOVD	n4+80(FP), R4
	CBZ	R4, imdct_prerotate_done

	MOVD	$0, R5

imdct_prerotate_loop:
	MOVD	R5, R6
	LSL	$4, R6
	ADD	R1, R6, R6
	FMOVD	(R6), F0
	FCVTDS	F0, F0

	MOVD	R3, R7
	SUB	$1, R7
	MOVD	R5, R8
	LSL	$1, R8
	SUB	R8, R7
	LSL	$3, R7
	ADD	R1, R7, R7
	FMOVD	(R7), F1
	FCVTDS	F1, F1

	MOVD	R5, R8
	LSL	$2, R8
	ADD	R2, R8, R8
	FMOVS	(R8), F2

	MOVD	R4, R9
	ADD	R5, R9
	LSL	$2, R9
	ADD	R2, R9, R9
	FMOVS	(R9), F3

	FMULS	F1, F2, F4
	FMULS	F0, F3, F5
	FADDS	F4, F5, F4

	FMULS	F0, F2, F6
	FMULS	F1, F3, F7
	FSUBS	F7, F6, F6

	MOVD	R5, R10
	LSL	$3, R10
	ADD	R0, R10, R10
	FMOVS	F6, (R10)
	FMOVS	F4, 4(R10)

	ADD	$1, R5
	CMP	R4, R5
	BLT	imdct_prerotate_loop

imdct_prerotate_done:
	RET
