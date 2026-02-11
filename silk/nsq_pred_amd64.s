#include "textflag.h"

// func shortTermPrediction16(sLPCQ14 []int32, idx int, aQ12 []int16) int32
//
// Go ABI0 frame layout on AMD64:
//   sLPCQ14_base+0(FP)   *int32
//   sLPCQ14_len+8(FP)    int
//   sLPCQ14_cap+16(FP)   int
//   idx+24(FP)            int
//   aQ12_base+32(FP)     *int16
//   aQ12_len+40(FP)      int
//   aQ12_cap+48(FP)      int
//   ret+56(FP)            int32
//
// Computes: 8 + sum((sLPCQ14[idx-k] * int16(aQ12[k])) >> 16) for k=0..15
//
// Register allocation:
//   SI = sLPCQ14 base (adjusted to &sLPCQ14[idx])
//   DI = aQ12 base pointer
//   AX = accumulator
//   DX = signal value (temp)
//   CX = coefficient value (temp)
//   R8 = temp product
TEXT ·shortTermPrediction16(SB), NOSPLIT|NOFRAME, $0-60
	MOVQ	sLPCQ14_base+0(FP), SI   // SI = &sLPCQ14[0]
	MOVQ	idx+24(FP), DX            // DX = idx
	MOVQ	aQ12_base+32(FP), DI      // DI = &aQ12[0]

	// SI = &sLPCQ14[idx] (each element is 4 bytes)
	LEAQ	(SI)(DX*4), SI

	// AX = accumulator, start with rounding bias 8
	MOVL	$8, AX

	// Tap 0: sLPCQ14[idx-0] * aQ12[0]
	MOVLQSX	(SI), DX                  // DX = sLPCQ14[idx] sign-extended to int64
	MOVWQSX	(DI), CX                  // CX = aQ12[0] sign-extended to int64
	IMULQ	CX, DX                    // DX = int64(DX) * int64(CX)
	SARQ	$16, DX                    // DX >>= 16
	ADDL	DX, AX                     // acc += int32(DX)

	// Tap 1
	MOVLQSX	-4(SI), DX
	MOVWQSX	2(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 2
	MOVLQSX	-8(SI), DX
	MOVWQSX	4(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 3
	MOVLQSX	-12(SI), DX
	MOVWQSX	6(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 4
	MOVLQSX	-16(SI), DX
	MOVWQSX	8(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 5
	MOVLQSX	-20(SI), DX
	MOVWQSX	10(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 6
	MOVLQSX	-24(SI), DX
	MOVWQSX	12(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 7
	MOVLQSX	-28(SI), DX
	MOVWQSX	14(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 8
	MOVLQSX	-32(SI), DX
	MOVWQSX	16(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 9
	MOVLQSX	-36(SI), DX
	MOVWQSX	18(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 10
	MOVLQSX	-40(SI), DX
	MOVWQSX	20(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 11
	MOVLQSX	-44(SI), DX
	MOVWQSX	22(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 12
	MOVLQSX	-48(SI), DX
	MOVWQSX	24(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 13
	MOVLQSX	-52(SI), DX
	MOVWQSX	26(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 14
	MOVLQSX	-56(SI), DX
	MOVWQSX	28(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 15
	MOVLQSX	-60(SI), DX
	MOVWQSX	30(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Store result
	MOVL	AX, ret+56(FP)
	RET

// func shortTermPrediction10(sLPCQ14 []int32, idx int, aQ12 []int16) int32
//
// Computes: 5 + sum((sLPCQ14[idx-k] * int16(aQ12[k])) >> 16) for k=0..9
TEXT ·shortTermPrediction10(SB), NOSPLIT|NOFRAME, $0-60
	MOVQ	sLPCQ14_base+0(FP), SI
	MOVQ	idx+24(FP), DX
	MOVQ	aQ12_base+32(FP), DI

	// SI = &sLPCQ14[idx]
	LEAQ	(SI)(DX*4), SI

	// AX = accumulator, start with rounding bias 5
	MOVL	$5, AX

	// Tap 0
	MOVLQSX	(SI), DX
	MOVWQSX	(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 1
	MOVLQSX	-4(SI), DX
	MOVWQSX	2(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 2
	MOVLQSX	-8(SI), DX
	MOVWQSX	4(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 3
	MOVLQSX	-12(SI), DX
	MOVWQSX	6(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 4
	MOVLQSX	-16(SI), DX
	MOVWQSX	8(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 5
	MOVLQSX	-20(SI), DX
	MOVWQSX	10(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 6
	MOVLQSX	-24(SI), DX
	MOVWQSX	12(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 7
	MOVLQSX	-28(SI), DX
	MOVWQSX	14(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 8
	MOVLQSX	-32(SI), DX
	MOVWQSX	16(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Tap 9
	MOVLQSX	-36(SI), DX
	MOVWQSX	18(DI), CX
	IMULQ	CX, DX
	SARQ	$16, DX
	ADDL	DX, AX

	// Store result
	MOVL	AX, ret+56(FP)
	RET
