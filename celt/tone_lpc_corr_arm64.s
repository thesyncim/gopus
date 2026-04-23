#include "textflag.h"

// func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32)
//
// Computes three float32 correlations for toneLPC in sequential accumulation
// order. The tone detector feeds exact postfilter header decisions, so avoid a
// vector reduction that changes rounding near period boundaries.
TEXT ·toneLPCCorr(SB), NOSPLIT, $0-56
	MOVD  x_base+0(FP), R0
	MOVD  cnt+24(FP), R1
	MOVD  delay+32(FP), R2
	MOVD  delay2+40(FP), R3

	FMOVS ZR, F3
	FMOVS ZR, F4
	FMOVS ZR, F5

	CMP   $1, R1
	BLT   store

	LSL   $2, R2, R2
	LSL   $2, R3, R3
	ADD   R0, R2, R5
	ADD   R0, R3, R6

loop:
	FMOVS (R0), F0
	FMOVS (R5), F1
	FMOVS (R6), F2
	FMULS F0, F0, F7
	FADDS F7, F3, F3
	FMULS F1, F0, F7
	FADDS F7, F4, F4
	FMULS F2, F0, F7
	FADDS F7, F5, F5
	ADD   $4, R0
	ADD   $4, R5
	ADD   $4, R6
	SUBS  $1, R1, R1
	BNE   loop

store:
	FMOVS F3, ret+48(FP)
	FMOVS F4, ret1+52(FP)
	FMOVS F5, ret2+56(FP)
	RET
