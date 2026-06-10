//go:build arm64 && !purego

#include "textflag.h"

// func xcorrKernel4Float32Neon4Acc(x, y []float32, sum *[4]float32, length int)
//
// Four-phase variant of the 4-lag cross-correlation kernel: samples are
// processed four at a time into four independent lane accumulators
// (acc0..acc3, one per sample phase), so the FMLA chains run in parallel
// instead of serializing on a single accumulator. acc0 is seeded with the
// caller's sums; after the blocked loop the lanes combine as
// (acc0+acc1)+(acc2+acc3) and a scalar-style tail finishes samples length%4.
// y must expose length+3 readable elements, like xcorrKernel4Float32Neon.
// xcorrKernel4Float32FourAccRef is the order-matched Go reference.
TEXT ·xcorrKernel4Float32Neon4Acc(SB), NOSPLIT, $0-64
	MOVD x_base+0(FP), R0
	MOVD y_base+24(FP), R1
	MOVD sum+48(FP), R2
	MOVD length+56(FP), R6

	// acc0 <- existing sum[0..3]; acc1..acc3 <- 0
	VLD1 (R2), [V0.S4]
	VEOR V1.B16, V1.B16, V1.B16
	VEOR V2.B16, V2.B16, V2.B16
	VEOR V3.B16, V3.B16, V3.B16

	CMP $4, R6
	BLT tail

	// Shifted y window bases: R3=y+1, R4=y+2, R5=y+3 (floats).
	ADD $4, R1, R3
	ADD $8, R1, R4
	ADD $12, R1, R5

block4:
	VLD1.P 16(R0), [V4.S4] // x[i:i+4]
	VLD1.P 16(R1), [V5.S4] // y[i:i+4]
	VLD1.P 16(R3), [V6.S4] // y[i+1:i+5]
	VLD1.P 16(R4), [V7.S4] // y[i+2:i+6]
	VLD1.P 16(R5), [V8.S4] // y[i+3:i+7]

	VDUP  V4.S[0], V16.S4
	VFMLA V16.S4, V5.S4, V0.S4
	VDUP  V4.S[1], V17.S4
	VFMLA V17.S4, V6.S4, V1.S4
	VDUP  V4.S[2], V18.S4
	VFMLA V18.S4, V7.S4, V2.S4
	VDUP  V4.S[3], V19.S4
	VFMLA V19.S4, V8.S4, V3.S4

	SUB $4, R6
	CMP $4, R6
	BGE block4

	// Combine lanes: acc0 = (acc0+acc1) + (acc2+acc3). The Go assembler has
	// no vector FADD mnemonic, so the three adds are WORD-encoded.
	WORD $0x4E21D400 // FADD V0.4S, V0.4S, V1.4S
	WORD $0x4E23D442 // FADD V2.4S, V2.4S, V3.4S
	WORD $0x4E22D400 // FADD V0.4S, V0.4S, V2.4S

tail:
	CBZ R6, store

tailloop:
	FMOVS (R0), F4
	VDUP  V4.S[0], V5.S4
	VLD1  (R1), [V2.S4]
	VFMLA V5.S4, V2.S4, V0.S4

	ADD $4, R0
	ADD $4, R1
	SUB $1, R6
	CBNZ R6, tailloop

store:
	VST1 [V0.S4], (R2)
	RET
