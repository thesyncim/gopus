#include "textflag.h"

// func warpedARFeedback24(sAR2Q14 *[24]int32, diffQ14 int32, arShpQ13 []int16, warpQ16 int32) int32
//
// Go ABI0 frame layout on ARM64:
//   sAR2Q14+0(FP)        *[24]int32
//   diffQ14+8(FP)        int32
//   arShpQ13_base+16(FP) *int16
//   arShpQ13_len+24(FP)  int
//   arShpQ13_cap+32(FP)  int
//   warpQ16+40(FP)       int32
//   ret+48(FP)           int32
//
// Computes the 24-tap warped AR noise shaping feedback.
// Updates sAR2Q14 in-place, returns nARQ14 accumulator.
//
// Algorithm (per tap pair j, j+1):
//   tmp2 = sAR[j-1] + ((sAR[j] - tmp1) * warp) >> 16
//   sAR[j-1] = tmp1;  acc += (tmp1 * coef[j-1]) >> 16
//   tmp1 = sAR[j]   + ((sAR[j+1] - tmp2) * warp) >> 16
//   sAR[j] = tmp2;    acc += (tmp2 * coef[j]) >> 16
//
// Register allocation:
//   R0  = sAR2Q14 base pointer
//   R1  = arShpQ13 base pointer
//   R2  = warpQ16 (sign-extended to 64-bit for SMULL)
//   R3  = accumulator (nARQ14)
//   R4  = tmp1
//   R5  = tmp2
//   R6  = scratch (sAR load, diff, product)
//   R7  = scratch (coef load, product)
//   R8  = scratch
TEXT ·warpedARFeedback24(SB), NOSPLIT|NOFRAME, $0-52
	MOVD	sAR2Q14+0(FP), R0        // R0 = &sAR[0]
	MOVW	diffQ14+8(FP), R5         // R5 = diffQ14 (tmp2 initial = diffQ14)
	MOVD	arShpQ13_base+16(FP), R1  // R1 = &arShpQ13[0]
	MOVW	warpQ16+40(FP), R2        // R2 = warpQ16 (sign-extended by MOVW)

	// Sign-extend warpQ16 to 64-bit for SMULL
	SXTW	R2, R2

	// Initial step:
	//   tmp2 = diffQ14 + (sAR[0] * warp) >> 16
	//   tmp1 = sAR[0] + ((sAR[1] - tmp2) * warp) >> 16
	//   sAR[0] = tmp2
	//   acc = 12 + (tmp2 * arShpQ13[0]) >> 16
	MOVW	(R0), R4                  // R4 = sAR[0]
	SXTW	R4, R6                    // R6 = int64(sAR[0])
	SMULL	R2, R6, R6                // R6 = sAR[0] * warp
	ASR	$16, R6, R6               // R6 >>= 16
	ADD	R6, R5, R5                // tmp2 = diffQ14 + (sAR[0]*warp)>>16

	MOVW	4(R0), R6                 // R6 = sAR[1]
	SUBW	R5, R6, R8                // R8 = sAR[1] - tmp2
	SXTW	R8, R8
	SMULL	R2, R8, R8                // R8 = (sAR[1]-tmp2) * warp
	ASR	$16, R8, R8
	ADDW	R8, R4, R4                // tmp1 = sAR[0] + ((sAR[1]-tmp2)*warp)>>16

	MOVW	R5, (R0)                  // sAR[0] = tmp2

	// acc = 12 + (tmp2 * arShpQ13[0]) >> 16
	MOVW	$12, R3                   // R3 = acc = 12
	MOVH	(R1), R7                  // R7 = arShpQ13[0] sign-extended
	SXTW	R5, R6
	SMULL	R7, R6, R6                // R6 = tmp2 * arShpQ13[0]
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	// Macro-like pattern for each pair (j, j+1) starting from j=2
	// At entry: R4=tmp1, R5=tmp2 (from previous)
	// We process: sAR[j-1] gets tmp1, sAR[j] gets tmp2

// --- Pair j=2,3 ---
	MOVW	4(R0), R6                 // R6 = sAR[1] (will be overwritten)
	MOVW	8(R0), R8                 // R8 = sAR[2]
	// tmp2 = sAR[1] + ((sAR[2] - tmp1) * warp) >> 16
	SUBW	R4, R8, R8                // R8 = sAR[2] - tmp1
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5                // tmp2 = sAR[1] + ...
	MOVW	R4, 4(R0)                 // sAR[1] = tmp1
	// acc += (tmp1 * arShpQ13[1]) >> 16
	MOVH	2(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	// tmp1 = sAR[2] + ((sAR[3] - tmp2) * warp) >> 16
	MOVW	8(R0), R6                 // R6 = sAR[2]
	MOVW	12(R0), R8                // R8 = sAR[3]
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4                // tmp1 = sAR[2] + ...
	MOVW	R5, 8(R0)                 // sAR[2] = tmp2
	// acc += (tmp2 * arShpQ13[2]) >> 16
	MOVH	4(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=4,5 ---
	MOVW	12(R0), R6
	MOVW	16(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 12(R0)
	MOVH	6(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	16(R0), R6
	MOVW	20(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 16(R0)
	MOVH	8(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=6,7 ---
	MOVW	20(R0), R6
	MOVW	24(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 20(R0)
	MOVH	10(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	24(R0), R6
	MOVW	28(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 24(R0)
	MOVH	12(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=8,9 ---
	MOVW	28(R0), R6
	MOVW	32(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 28(R0)
	MOVH	14(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	32(R0), R6
	MOVW	36(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 32(R0)
	MOVH	16(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=10,11 ---
	MOVW	36(R0), R6
	MOVW	40(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 36(R0)
	MOVH	18(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	40(R0), R6
	MOVW	44(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 40(R0)
	MOVH	20(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=12,13 ---
	MOVW	44(R0), R6
	MOVW	48(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 44(R0)
	MOVH	22(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	48(R0), R6
	MOVW	52(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 48(R0)
	MOVH	24(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=14,15 ---
	MOVW	52(R0), R6
	MOVW	56(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 52(R0)
	MOVH	26(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	56(R0), R6
	MOVW	60(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 56(R0)
	MOVH	28(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=16,17 ---
	MOVW	60(R0), R6
	MOVW	64(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 60(R0)
	MOVH	30(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	64(R0), R6
	MOVW	68(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 64(R0)
	MOVH	32(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=18,19 ---
	MOVW	68(R0), R6
	MOVW	72(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 68(R0)
	MOVH	34(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	72(R0), R6
	MOVW	76(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 72(R0)
	MOVH	36(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=20,21 ---
	MOVW	76(R0), R6
	MOVW	80(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 76(R0)
	MOVH	38(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	80(R0), R6
	MOVW	84(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 80(R0)
	MOVH	40(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=22,23 (last pair) ---
	MOVW	84(R0), R6
	MOVW	88(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 84(R0)                // sAR[21] = tmp1
	MOVH	42(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	88(R0), R6                // R6 = sAR[22]
	MOVW	92(R0), R8                // R8 = sAR[23]
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4                // tmp1 = sAR[22] + ((sAR[23]-tmp2)*warp)>>16
	MOVW	R5, 88(R0)                // sAR[22] = tmp2
	MOVH	44(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	// Final tap: sAR[23] = tmp1, acc += (tmp1 * arShpQ13[23]) >> 16
	MOVW	R4, 92(R0)                // sAR[23] = tmp1
	MOVH	46(R1), R7                // arShpQ13[23]
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	R3, ret+48(FP)
	RET

// func warpedARFeedback16(sAR2Q14 *[24]int32, diffQ14 int32, arShpQ13 []int16, warpQ16 int32) int32
//
// Same algorithm as 24-tap but only 16 taps. Rounding bias = 8.
TEXT ·warpedARFeedback16(SB), NOSPLIT|NOFRAME, $0-52
	MOVD	sAR2Q14+0(FP), R0
	MOVW	diffQ14+8(FP), R5
	MOVD	arShpQ13_base+16(FP), R1
	MOVW	warpQ16+40(FP), R2
	SXTW	R2, R2

	// Initial step
	MOVW	(R0), R4
	SXTW	R4, R6
	SMULL	R2, R6, R6
	ASR	$16, R6, R6
	ADD	R6, R5, R5

	MOVW	4(R0), R6
	SUBW	R5, R6, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R4, R4

	MOVW	R5, (R0)

	MOVW	$8, R3
	MOVH	(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=2,3 ---
	MOVW	4(R0), R6
	MOVW	8(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 4(R0)
	MOVH	2(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	8(R0), R6
	MOVW	12(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 8(R0)
	MOVH	4(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=4,5 ---
	MOVW	12(R0), R6
	MOVW	16(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 12(R0)
	MOVH	6(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	16(R0), R6
	MOVW	20(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 16(R0)
	MOVH	8(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=6,7 ---
	MOVW	20(R0), R6
	MOVW	24(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 20(R0)
	MOVH	10(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	24(R0), R6
	MOVW	28(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 24(R0)
	MOVH	12(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=8,9 ---
	MOVW	28(R0), R6
	MOVW	32(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 28(R0)
	MOVH	14(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	32(R0), R6
	MOVW	36(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 32(R0)
	MOVH	16(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=10,11 ---
	MOVW	36(R0), R6
	MOVW	40(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 36(R0)
	MOVH	18(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	40(R0), R6
	MOVW	44(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 40(R0)
	MOVH	20(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=12,13 ---
	MOVW	44(R0), R6
	MOVW	48(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 44(R0)
	MOVH	22(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	48(R0), R6
	MOVW	52(R0), R8
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4
	MOVW	R5, 48(R0)
	MOVH	24(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

// --- Pair j=14, final tap 15 ---
	MOVW	52(R0), R6
	MOVW	56(R0), R8
	SUBW	R4, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R5
	MOVW	R4, 52(R0)                // sAR[13] = tmp1
	MOVH	26(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	56(R0), R6                // R6 = sAR[14]
	MOVW	60(R0), R8                // R8 = sAR[15]
	SUBW	R5, R8, R8
	SXTW	R8, R8
	SMULL	R2, R8, R8
	ASR	$16, R8, R8
	ADDW	R8, R6, R4                // tmp1 = sAR[14] + ((sAR[15]-tmp2)*warp)>>16
	MOVW	R5, 56(R0)                // sAR[14] = tmp2
	MOVH	28(R1), R7
	SXTW	R5, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	// Final: sAR[15] = tmp1, acc += (tmp1 * arShpQ13[15]) >> 16
	MOVW	R4, 60(R0)
	MOVH	30(R1), R7
	SXTW	R4, R6
	SMULL	R7, R6, R6
	ASR	$16, R6, R6
	ADDW	R6, R3, R3

	MOVW	R3, ret+48(FP)
	RET
