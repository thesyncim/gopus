#include "textflag.h"

// func shortTermPrediction16(sLPCQ14 []int32, idx int, aQ12 []int16) int32
TEXT ·shortTermPrediction16(SB), NOSPLIT, $0-64
	MOVD    sLPCQ14_base+0(FP), R0
	MOVD    idx+24(FP), R1
	MOVD    aQ12_base+32(FP), R2

	MOVD    $8, R5          // acc = 8
	MOVD    $16, R6         // loop = 16
	
	LSL     $2, R1, R10
	ADD     R0, R10, R3     // sLPC_ptr = base + idx*4
	MOVD    R2, R4          // a_ptr = base

loop16:
	MOVW    (R3), R7        // val_s
	SUB     $4, R3
	
	MOVH    (R4), R8        // val_a
	SXTH    R8, R8
	ADD     $2, R4
	
	SMULL   R7, R8, R9      // prod
	ASR     $16, R9, R9
	ADDW    R9, R5, R5
	
	SUB     $1, R6
	CBNZ    R6, loop16

	MOVD    R5, ret+56(FP)
	RET

// func shortTermPrediction10(sLPCQ14 []int32, idx int, aQ12 []int16) int32
TEXT ·shortTermPrediction10(SB), NOSPLIT, $0-64
	MOVD    sLPCQ14_base+0(FP), R0
	MOVD    idx+24(FP), R1
	MOVD    aQ12_base+32(FP), R2

	MOVD    $5, R5          // acc = 5
	MOVD    $10, R6         // loop = 10
	
	LSL     $2, R1, R10
	ADD     R0, R10, R3
	MOVD    R2, R4

loop10:
	MOVW    (R3), R7
	SUB     $4, R3
	
	MOVH    (R4), R8
	SXTH    R8, R8
	ADD     $2, R4
	
	SMULL   R7, R8, R9
	ASR     $16, R9, R9
	ADDW    R9, R5, R5
	
	SUB     $1, R6
	CBNZ    R6, loop10

	MOVD    R5, ret+56(FP)
	RET
