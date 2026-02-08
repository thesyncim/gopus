#include "textflag.h"

// func shortTermPrediction16(sLPCQ14 []int32, idx int, aQ12 []int16) int32
//
// Go ABI0 frame layout on ARM64:
//   sLPCQ14_base+0(FP)   *int32  (R0 via stack)
//   sLPCQ14_len+8(FP)    int
//   sLPCQ14_cap+16(FP)   int
//   idx+24(FP)            int
//   aQ12_base+32(FP)     *int16
//   aQ12_len+40(FP)      int
//   aQ12_cap+48(FP)      int
//   ret+56(FP)            int32
//
// Computes: 8 + sum((sLPCQ14[idx-k] * int16(aQ12[k])) >> 16) for k=0..15
// This is silk_SMLAWB: out += (int32 * int16) >> 16
//
// Register allocation:
//   R0 = sLPCQ14 base pointer (adjusted to &sLPCQ14[idx])
//   R1 = aQ12 base pointer
//   R2 = accumulator
//   R3, R5 = signal values (alternating)
//   R4, R6 = coefficient values (alternating)
//   R7 = temp product (alternating)
//   R8 = temp product (alternating)
//
// Optimization: ASR+ADD replaced with shifted ADD (ADD R6>>16, R3, R3)
TEXT ·shortTermPrediction16(SB), NOSPLIT|NOFRAME, $0-60
	MOVD	sLPCQ14_base+0(FP), R0   // R0 = &sLPCQ14[0]
	MOVD	idx+24(FP), R1            // R1 = idx
	MOVD	aQ12_base+32(FP), R2      // R2 = &aQ12[0]

	// R0 = &sLPCQ14[idx] (each element is 4 bytes)
	ADD	R1<<2, R0, R0

	// R3 = accumulator, start with rounding bias 8
	MOVW	$8, R3

	// Tap 0: sLPCQ14[idx-0] * aQ12[0]
	MOVW	(R0), R4                  // R4 = sLPCQ14[idx] (int32)
	MOVH	(R2), R5                  // R5 = aQ12[0] (int16 sign-extended)
	SMULL	R4, R5, R6                // R6 = int64(R4) * int64(R5)
	ADD	R6>>16, R3, R3            // acc += R6 >> 16

	// Tap 1: sLPCQ14[idx-1] * aQ12[1]
	MOVW	-4(R0), R4
	MOVH	2(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 2: sLPCQ14[idx-2] * aQ12[2]
	MOVW	-8(R0), R4
	MOVH	4(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 3: sLPCQ14[idx-3] * aQ12[3]
	MOVW	-12(R0), R4
	MOVH	6(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 4: sLPCQ14[idx-4] * aQ12[4]
	MOVW	-16(R0), R4
	MOVH	8(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 5: sLPCQ14[idx-5] * aQ12[5]
	MOVW	-20(R0), R4
	MOVH	10(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 6: sLPCQ14[idx-6] * aQ12[6]
	MOVW	-24(R0), R4
	MOVH	12(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 7: sLPCQ14[idx-7] * aQ12[7]
	MOVW	-28(R0), R4
	MOVH	14(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 8: sLPCQ14[idx-8] * aQ12[8]
	MOVW	-32(R0), R4
	MOVH	16(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 9: sLPCQ14[idx-9] * aQ12[9]
	MOVW	-36(R0), R4
	MOVH	18(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 10: sLPCQ14[idx-10] * aQ12[10]
	MOVW	-40(R0), R4
	MOVH	20(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 11: sLPCQ14[idx-11] * aQ12[11]
	MOVW	-44(R0), R4
	MOVH	22(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 12: sLPCQ14[idx-12] * aQ12[12]
	MOVW	-48(R0), R4
	MOVH	24(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 13: sLPCQ14[idx-13] * aQ12[13]
	MOVW	-52(R0), R4
	MOVH	26(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 14: sLPCQ14[idx-14] * aQ12[14]
	MOVW	-56(R0), R4
	MOVH	28(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 15: sLPCQ14[idx-15] * aQ12[15]
	MOVW	-60(R0), R4
	MOVH	30(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Store result
	MOVW	R3, ret+56(FP)
	RET

// func shortTermPrediction10(sLPCQ14 []int32, idx int, aQ12 []int16) int32
//
// Computes: 5 + sum((sLPCQ14[idx-k] * int16(aQ12[k])) >> 16) for k=0..9
TEXT ·shortTermPrediction10(SB), NOSPLIT|NOFRAME, $0-60
	MOVD	sLPCQ14_base+0(FP), R0
	MOVD	idx+24(FP), R1
	MOVD	aQ12_base+32(FP), R2

	// R0 = &sLPCQ14[idx]
	ADD	R1<<2, R0, R0

	// R3 = accumulator, start with rounding bias 5
	MOVW	$5, R3

	// Tap 0
	MOVW	(R0), R4
	MOVH	(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 1
	MOVW	-4(R0), R4
	MOVH	2(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 2
	MOVW	-8(R0), R4
	MOVH	4(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 3
	MOVW	-12(R0), R4
	MOVH	6(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 4
	MOVW	-16(R0), R4
	MOVH	8(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 5
	MOVW	-20(R0), R4
	MOVH	10(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 6
	MOVW	-24(R0), R4
	MOVH	12(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 7
	MOVW	-28(R0), R4
	MOVH	14(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 8
	MOVW	-32(R0), R4
	MOVH	16(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Tap 9
	MOVW	-36(R0), R4
	MOVH	18(R2), R5
	SMULL	R4, R5, R6
	ADD	R6>>16, R3, R3

	// Store result
	MOVW	R3, ret+56(FP)
	RET
