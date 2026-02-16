#include "textflag.h"

// func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32)
//
// Computes three float32 correlations for toneLPC using scalar FMADDS
// in exact sequential accumulation order to match Go compiler output.
// Eliminates bounds checks for speed.
//
// Register allocation:
//   R0    = x base pointer
//   R1    = cnt
//   R2    = delay (element offset)
//   R3    = delay2 (element offset)
//   R4    = loop counter i
//   R5,R6 = address temporaries
//   F0    = r00 accumulator
//   F1    = r01 accumulator
//   F2    = r02 accumulator
//   F3    = x[i]
//   F4    = x[i+delay]
//   F5    = x[i+delay2]
TEXT ·toneLPCCorr(SB), NOSPLIT, $0-56
	MOVD  x_base+0(FP), R0
	MOVD  cnt+24(FP), R1
	MOVD  delay+32(FP), R2
	MOVD  delay2+40(FP), R3

	// Zero accumulators
	FMOVS ZR, F0                  // r00 = 0
	FMOVS ZR, F1                  // r01 = 0
	FMOVS ZR, F2                  // r02 = 0

	CMP   $1, R1
	BLT   store

	// Byte offsets for delayed pointers
	LSL   $2, R2, R2              // delay * sizeof(float32)
	LSL   $2, R3, R3              // delay2 * sizeof(float32)

	MOVD  ZR, R4                  // i = 0

loop:
	// Load x[i]
	FMOVS (R0), F3

	// Load x[i+delay]: R5 = R0 + delay_bytes
	ADD   R0, R2, R5
	FMOVS (R5), F4

	// Load x[i+delay2]: R5 = R0 + delay2_bytes
	ADD   R0, R3, R5
	FMOVS (R5), F5

	// r00 += x[i] * x[i]
	FMADDS F3, F0, F3, F0

	// r01 += x[i] * x[i+delay]
	FMADDS F4, F1, F3, F1

	// r02 += x[i] * x[i+delay2]
	FMADDS F5, F2, F3, F2

	ADD   $4, R0                  // advance pointer by sizeof(float32)
	ADD   $1, R4
	CMP   R1, R4
	BLT   loop

store:
	FMOVS F0, ret+48(FP)
	FMOVS F1, ret1+52(FP)
	FMOVS F2, ret2+56(FP)
	RET
