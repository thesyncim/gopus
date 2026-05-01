//go:build arm64

#include "textflag.h"

// func scaleFloat64Into(dst, src []float64, scale float64, n int)
TEXT ·scaleFloat64Into(SB), NOSPLIT, $0-64
	MOVD  dst_base+0(FP), R0
	MOVD  src_base+24(FP), R1
	FMOVD scale+48(FP), F0
	MOVD  n+56(FP), R2

	CBZ R2, done
	WORD $0x4E080400 // DUP V0.2D, V0.D[0]

	LSR $3, R2, R3
	AND $7, R2, R2
	CBZ R3, loop4_check

loop8:
	VLD1.P 32(R1), [V1.D2, V2.D2]
	WORD   $0x6E60DC21 // FMUL V1.2D, V1.2D, V0.2D
	WORD   $0x6E60DC42 // FMUL V2.2D, V2.2D, V0.2D
	VST1.P [V1.D2, V2.D2], 32(R0)
	VLD1.P 32(R1), [V1.D2, V2.D2]
	WORD   $0x6E60DC21 // FMUL V1.2D, V1.2D, V0.2D
	WORD   $0x6E60DC42 // FMUL V2.2D, V2.2D, V0.2D
	VST1.P [V1.D2, V2.D2], 32(R0)
	SUBS   $1, R3
	BNE    loop8

loop4_check:
	LSR $2, R2, R3
	AND $3, R2, R2
	CBZ R3, pair

loop4:
	VLD1.P 32(R1), [V1.D2, V2.D2]
	WORD   $0x6E60DC21 // FMUL V1.2D, V1.2D, V0.2D
	WORD   $0x6E60DC42 // FMUL V2.2D, V2.2D, V0.2D
	VST1.P [V1.D2, V2.D2], 32(R0)
	SUBS   $1, R3
	BNE    loop4

pair:
	LSR $1, R2, R3
	AND $1, R2, R2
	CBZ R3, tail
	VLD1.P 16(R1), [V1.D2]
	WORD   $0x6E60DC21 // FMUL V1.2D, V1.2D, V0.2D
	VST1.P [V1.D2], 16(R0)

tail:
	CBZ R2, done
	FMOVD (R1), F1
	FMULD F0, F1, F1
	FMOVD F1, (R0)

done:
	RET
