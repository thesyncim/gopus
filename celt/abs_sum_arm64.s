//go:build arm64 && !purego
#include "textflag.h"

// func absSum(x []float64) float64
//
// Computes sum of absolute values of a float64 slice. Keep the accumulation
// order identical to the scalar Go reference; callers use this helper in
// threshold decisions where tiny reassociation changes can alter branch flow.
TEXT ·absSum(SB), NOSPLIT, $0-32
	MOVD  x_base+0(FP), R0
	MOVD  x_len+8(FP), R1

	FMOVD ZR, F0

	// If length == 0, return 0
	CBZ   R1, as_done

as_loop:
	FMOVD (R0), F1
	ADD   $8, R0
	FABSD F1, F1
	FADDD F1, F0, F0
	SUB   $1, R1
	CBNZ  R1, as_loop

as_done:
	FMOVD  F0, ret+24(FP)
	RET
