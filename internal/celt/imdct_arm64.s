//go:build arm64 && !purego

#include "textflag.h"

// func imdctPostRotateF32(buf []float32, trig []float32, n2, n4 int)
TEXT ·imdctPostRotateF32(SB), NOSPLIT, $0-64
	MOVD	buf_base+0(FP), R0
	MOVD	trig_base+24(FP), R1
	MOVD	n2+48(FP), R2
	MOVD	n4+56(FP), R3

	MOVD	R3, R4
	ADD	$1, R4
	LSR	$1, R4
	CBZ	R4, imdct_postrotate_done

	MOVD	$0, R5
	MOVD	R2, R6
	SUB	$2, R6
	MOVD	$0, R7

imdct_postrotate_loop:
	MOVD	R5, R8
	LSL	$2, R8
	ADD	R0, R8, R9
	FMOVS	4(R9), F0
	FMOVS	(R9), F1

	MOVD	R7, R10
	LSL	$2, R10
	ADD	R1, R10, R11
	FMOVS	(R11), F2

	MOVD	R3, R12
	ADD	R7, R12
	LSL	$2, R12
	ADD	R1, R12, R13
	FMOVS	(R13), F3

	FMULS	F0, F2, F4
	FMULS	F1, F3, F5
	FADDS	F4, F5, F4

	FMULS	F0, F3, F6
	FMULS	F1, F2, F7
	FSUBS	F7, F6, F6

	MOVD	R6, R14
	LSL	$2, R14
	ADD	R0, R14, R15
	FMOVS	4(R15), F8
	FMOVS	(R15), F9

	FMOVS	F4, (R9)
	FMOVS	F6, 4(R15)

	MOVD	R3, R12
	SUB	R7, R12
	SUB	$1, R12
	LSL	$2, R12
	ADD	R1, R12, R13
	FMOVS	(R13), F2

	MOVD	R2, R16
	SUB	R7, R16
	SUB	$1, R16
	LSL	$2, R16
	ADD	R1, R16, R17
	FMOVS	(R17), F3

	FMULS	F8, F2, F4
	FMULS	F9, F3, F5
	FADDS	F4, F5, F4

	FMULS	F8, F3, F6
	FMULS	F9, F2, F7
	FSUBS	F7, F6, F6

	FMOVS	F4, (R15)
	FMOVS	F6, 4(R9)

	ADD	$2, R5
	SUB	$2, R6
	ADD	$1, R7
	CMP	R4, R7
	BLT	imdct_postrotate_loop

imdct_postrotate_done:
	RET
