#include "textflag.h"

// func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int)
//
// Vectorized 4-way pitch cross-correlation using ARM64 NEON.
// 2x unrolled inner loop with dual accumulators (V16-V19 + V20-V23) to hide
// the 4-cycle VFMLA latency. VLD1.P post-increment loads eliminate pointer ADDs.
TEXT ·celtPitchXcorr(SB), NOSPLIT, $0-88
	MOVD x_base+0(FP), R0
	MOVD y_base+24(FP), R1
	MOVD xcorr_base+48(FP), R2
	MOVD length+72(FP), R3
	MOVD maxPitch+80(FP), R4

	CMP  $1, R3
	BLT  done
	CMP  $1, R4
	BLT  done

	MOVD $16, R15
	MOVD ZR, R5
	SUBS $3, R4, R6
	BLE  outer_tail

outer4:
	// Zero 8 accumulators: a (V16-V19) and b (V20-V23)
	VEOR V16.B16, V16.B16, V16.B16
	VEOR V17.B16, V17.B16, V17.B16
	VEOR V18.B16, V18.B16, V18.B16
	VEOR V19.B16, V19.B16, V19.B16
	VEOR V20.B16, V20.B16, V20.B16
	VEOR V21.B16, V21.B16, V21.B16
	VEOR V22.B16, V22.B16, V22.B16
	VEOR V23.B16, V23.B16, V23.B16

	// Setup 5 pointers
	MOVD R0, R7
	LSL  $3, R5, R13
	ADD  R1, R13, R8
	ADD  $8, R8, R9
	ADD  $16, R8, R10
	ADD  $24, R8, R11

	// R12 = (length / 4) iteration count for 2x unrolled loop
	LSR  $2, R3, R12
	CBZ  R12, tail4_pair

inner4:
	// Batch A → V16-V19
	VLD1.P (R7)(R15), [V0.D2]
	VLD1.P (R8)(R15), [V1.D2]
	VLD1.P (R9)(R15), [V2.D2]
	VLD1.P (R10)(R15), [V3.D2]
	VLD1.P (R11)(R15), [V4.D2]
	VFMLA V0.D2, V1.D2, V16.D2
	VFMLA V0.D2, V2.D2, V17.D2
	VFMLA V0.D2, V3.D2, V18.D2
	VFMLA V0.D2, V4.D2, V19.D2
	// Batch B → V20-V23
	VLD1.P (R7)(R15), [V5.D2]
	VLD1.P (R8)(R15), [V6.D2]
	VLD1.P (R9)(R15), [V7.D2]
	VLD1.P (R10)(R15), [V8.D2]
	VLD1.P (R11)(R15), [V9.D2]
	VFMLA V5.D2, V6.D2, V20.D2
	VFMLA V5.D2, V7.D2, V21.D2
	VFMLA V5.D2, V8.D2, V22.D2
	VFMLA V5.D2, V9.D2, V23.D2
	SUBS $1, R12, R12
	BNE  inner4

tail4_pair:
	// Handle 2-element remainder (length%4 >= 2)
	AND  $2, R3, R13
	CBZ  R13, reduce4

	VLD1.P (R7)(R15), [V0.D2]
	VLD1.P (R8)(R15), [V1.D2]
	VLD1.P (R9)(R15), [V2.D2]
	VLD1.P (R10)(R15), [V3.D2]
	VLD1.P (R11)(R15), [V4.D2]
	VFMLA V0.D2, V1.D2, V16.D2
	VFMLA V0.D2, V2.D2, V17.D2
	VFMLA V0.D2, V3.D2, V18.D2
	VFMLA V0.D2, V4.D2, V19.D2

reduce4:
	// Horizontal reduce + combine dual accumulators using scalar FADDD
	// acc0: V16(a) + V20(b)
	VEXT  $8, V16.B16, V16.B16, V0.B16
	FADDD F0, F16, F16
	VEXT  $8, V20.B16, V20.B16, V0.B16
	FADDD F0, F20, F20
	FADDD F20, F16, F16
	// acc1: V17(a) + V21(b)
	VEXT  $8, V17.B16, V17.B16, V1.B16
	FADDD F1, F17, F17
	VEXT  $8, V21.B16, V21.B16, V1.B16
	FADDD F1, F21, F21
	FADDD F21, F17, F17
	// acc2: V18(a) + V22(b)
	VEXT  $8, V18.B16, V18.B16, V2.B16
	FADDD F2, F18, F18
	VEXT  $8, V22.B16, V22.B16, V2.B16
	FADDD F2, F22, F22
	FADDD F22, F18, F18
	// acc3: V19(a) + V23(b)
	VEXT  $8, V19.B16, V19.B16, V3.B16
	FADDD F3, F19, F19
	VEXT  $8, V23.B16, V23.B16, V3.B16
	FADDD F3, F23, F23
	FADDD F23, F19, F19

	// Odd trailing element
	AND  $1, R3, R13
	CBZ  R13, store4

	FMOVD (R7), F0
	FMOVD (R8), F1
	FMOVD (R9), F2
	FMOVD (R10), F3
	FMOVD (R11), F4
	FMADDD F1, F16, F0, F16
	FMADDD F2, F17, F0, F17
	FMADDD F3, F18, F0, F18
	FMADDD F4, F19, F0, F19

store4:
	LSL   $3, R5, R13
	ADD   R2, R13, R14
	FMOVD F16, (R14)
	FMOVD F17, 8(R14)
	FMOVD F18, 16(R14)
	FMOVD F19, 24(R14)

	ADD $4, R5
	CMP R6, R5
	BLT outer4

outer_tail:
	CMP R4, R5
	BGE done

outer1:
	VEOR V16.B16, V16.B16, V16.B16

	MOVD R0, R7
	LSL  $3, R5, R13
	ADD  R1, R13, R8

	AND  $1, R3, R13
	SUB  R13, R3, R12
	CBZ  R12, reduce1

inner1:
	VLD1.P (R7)(R15), [V0.D2]
	VLD1.P (R8)(R15), [V1.D2]
	VFMLA V0.D2, V1.D2, V16.D2
	SUBS $2, R12, R12
	BNE  inner1

reduce1:
	VEXT  $8, V16.B16, V16.B16, V0.B16
	FADDD F0, F16, F16

	AND  $1, R3, R13
	CBZ  R13, store1

	FMOVD  (R7), F0
	FMOVD  (R8), F1
	FMADDD F1, F16, F0, F16

store1:
	LSL   $3, R5, R13
	ADD   R2, R13, R14
	FMOVD F16, (R14)

	ADD $1, R5
	CMP R4, R5
	BLT outer1

done:
	RET
