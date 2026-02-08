#include "textflag.h"

// FMADDD operand order in Go Plan 9 ARM64:
//   FMADDD Fm, Fa, Fn, Fd → Fd = Fa + Fn * Fm
// So for sum += x * y: FMADDD Fy, Fsum, Fx, Fsum

// func celtInnerProd(x, y []float64, length int) float64
TEXT ·celtInnerProd(SB), NOSPLIT, $0-64
	MOVD length+48(FP), R2
	CMP $1, R2
	BLT celtInnerProd_zero

	MOVD x_base+0(FP), R0
	MOVD y_base+24(FP), R1

	FMOVD ZR, F0  // sum0
	FMOVD ZR, F1  // sum1
	FMOVD ZR, F2  // sum2
	FMOVD ZR, F3  // sum3

	CMP $4, R2
	BLT celtInnerProd_tail

celtInnerProd_loop4:
	FMOVD (R0), F4
	FMOVD (R1), F5
	FMADDD F5, F0, F4, F0   // F0 += F4 * F5

	FMOVD 8(R0), F4
	FMOVD 8(R1), F5
	FMADDD F5, F1, F4, F1

	FMOVD 16(R0), F4
	FMOVD 16(R1), F5
	FMADDD F5, F2, F4, F2

	FMOVD 24(R0), F4
	FMOVD 24(R1), F5
	FMADDD F5, F3, F4, F3

	ADD $32, R0
	ADD $32, R1
	SUB $4, R2
	CMP $4, R2
	BGE celtInnerProd_loop4

celtInnerProd_tail:
	CBZ R2, celtInnerProd_done

celtInnerProd_tail_loop:
	FMOVD (R0), F4
	FMOVD (R1), F5
	FMADDD F5, F0, F4, F0
	ADD $8, R0
	ADD $8, R1
	SUB $1, R2
	CBNZ R2, celtInnerProd_tail_loop

celtInnerProd_done:
	FADDD F1, F0, F0
	FADDD F2, F0, F0
	FADDD F3, F0, F0
	FMOVD F0, ret+56(FP)
	RET

celtInnerProd_zero:
	FMOVD ZR, F0
	FMOVD F0, ret+56(FP)
	RET

// func dualInnerProd(x, y1, y2 []float64, length int) (float64, float64)
TEXT ·dualInnerProd(SB), NOSPLIT, $0-96
	MOVD length+72(FP), R3
	CMP $1, R3
	BLT dualInnerProd_zero

	MOVD x_base+0(FP), R0
	MOVD y1_base+24(FP), R1
	MOVD y2_base+48(FP), R2

	FMOVD ZR, F0  // sum1a
	FMOVD ZR, F1  // sum2a
	FMOVD ZR, F2  // sum1b
	FMOVD ZR, F3  // sum2b

	CMP $2, R3
	BLT dualInnerProd_tail

dualInnerProd_loop2:
	// Element 0
	FMOVD (R0), F4
	FMOVD (R1), F5
	FMOVD (R2), F6
	FMADDD F5, F0, F4, F0   // sum1a += x * y1
	FMADDD F6, F1, F4, F1   // sum2a += x * y2

	// Element 1
	FMOVD 8(R0), F4
	FMOVD 8(R1), F5
	FMOVD 8(R2), F6
	FMADDD F5, F2, F4, F2   // sum1b += x * y1
	FMADDD F6, F3, F4, F3   // sum2b += x * y2

	ADD $16, R0
	ADD $16, R1
	ADD $16, R2
	SUB $2, R3
	CMP $2, R3
	BGE dualInnerProd_loop2

dualInnerProd_tail:
	CBZ R3, dualInnerProd_done

	FMOVD (R0), F4
	FMOVD (R1), F5
	FMOVD (R2), F6
	FMADDD F5, F0, F4, F0
	FMADDD F6, F1, F4, F1

dualInnerProd_done:
	FADDD F2, F0, F0  // sum1 = sum1a + sum1b
	FADDD F3, F1, F1  // sum2 = sum2a + sum2b
	FMOVD F0, ret+80(FP)
	FMOVD F1, ret1+88(FP)
	RET

dualInnerProd_zero:
	FMOVD ZR, F0
	FMOVD F0, ret+80(FP)
	FMOVD F0, ret1+88(FP)
	RET

// func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int)
TEXT ·celtPitchXcorr(SB), NOSPLIT, $0-88
	MOVD length+72(FP), R3
	MOVD maxPitch+80(FP), R4
	CMP $1, R3
	BLT celtPitchXcorr_done
	CMP $1, R4
	BLT celtPitchXcorr_done

	MOVD x_base+0(FP), R0
	MOVD y_base+24(FP), R1
	MOVD xcorr_base+48(FP), R2

	MOVD ZR, R5       // i = 0
	SUB $3, R4, R6    // R6 = maxPitch - 3
	CMP $4, R4
	BLT celtPitchXcorr_tail_outer

celtPitchXcorr_outer4:
	FMOVD ZR, F0   // s0a
	FMOVD ZR, F1   // s1a
	FMOVD ZR, F2   // s2a
	FMOVD ZR, F3   // s3a
	FMOVD ZR, F17  // s0b
	FMOVD ZR, F18  // s1b
	FMOVD ZR, F19  // s2b
	FMOVD ZR, F20  // s3b

	MOVD R0, R7           // x_ptr = x_base
	ADD R5<<3, R1, R8     // y_ptr = &y[i]
	MOVD R3, R9           // inner count = length

	CMP $2, R9
	BLT celtPitchXcorr_inner_tail

celtPitchXcorr_inner2:
	// Load two x values
	FMOVD (R7), F4        // x[j]
	FMOVD 8(R7), F21      // x[j+1]

	// Load 5 y values (sharing y[i+j+1..i+j+3])
	FMOVD (R8), F5        // y[i+j]
	FMOVD 8(R8), F6       // y[i+j+1]
	FMOVD 16(R8), F7      // y[i+j+2]
	FMOVD 24(R8), F16     // y[i+j+3]
	FMOVD 32(R8), F22     // y[i+j+4]

	// x[j] contributions
	FMADDD F5, F0, F4, F0    // s0a += x[j]*y[i+j]
	FMADDD F6, F1, F4, F1    // s1a += x[j]*y[i+j+1]
	FMADDD F7, F2, F4, F2    // s2a += x[j]*y[i+j+2]
	FMADDD F16, F3, F4, F3   // s3a += x[j]*y[i+j+3]

	// x[j+1] contributions (reuse F6, F7, F16)
	FMADDD F6, F17, F21, F17   // s0b += x[j+1]*y[i+j+1]
	FMADDD F7, F18, F21, F18   // s1b += x[j+1]*y[i+j+2]
	FMADDD F16, F19, F21, F19  // s2b += x[j+1]*y[i+j+3]
	FMADDD F22, F20, F21, F20  // s3b += x[j+1]*y[i+j+4]

	ADD $16, R7   // advance x by 2
	ADD $16, R8   // advance y by 2
	SUB $2, R9
	CMP $2, R9
	BGE celtPitchXcorr_inner2

celtPitchXcorr_inner_tail:
	CBZ R9, celtPitchXcorr_inner_done

	// Handle last odd element
	FMOVD (R7), F4
	FMOVD (R8), F5
	FMOVD 8(R8), F6
	FMOVD 16(R8), F7
	FMOVD 24(R8), F16
	FMADDD F5, F0, F4, F0
	FMADDD F6, F1, F4, F1
	FMADDD F7, F2, F4, F2
	FMADDD F16, F3, F4, F3

celtPitchXcorr_inner_done:
	// Combine dual accumulators
	FADDD F17, F0, F0
	FADDD F18, F1, F1
	FADDD F19, F2, F2
	FADDD F20, F3, F3

	// Store xcorr[i..i+3]
	ADD R5<<3, R2, R7
	FMOVD F0, (R7)
	FMOVD F1, 8(R7)
	FMOVD F2, 16(R7)
	FMOVD F3, 24(R7)

	ADD $4, R5
	CMP R5, R6
	BGT celtPitchXcorr_outer4

celtPitchXcorr_tail_outer:
	CMP R4, R5
	BGE celtPitchXcorr_done

celtPitchXcorr_tail_one:
	FMOVD ZR, F0
	MOVD R0, R7
	ADD R5<<3, R1, R8
	MOVD R3, R9

celtPitchXcorr_tail_inner:
	FMOVD (R7), F4
	FMOVD (R8), F5
	FMADDD F5, F0, F4, F0   // sum += x[j]*y[i+j]
	ADD $8, R7
	ADD $8, R8
	SUB $1, R9
	CBNZ R9, celtPitchXcorr_tail_inner

	ADD R5<<3, R2, R7
	FMOVD F0, (R7)

	ADD $1, R5
	CMP R4, R5
	BLT celtPitchXcorr_tail_one

celtPitchXcorr_done:
	RET
