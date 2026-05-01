//go:build arm64

#include "textflag.h"

// func deemphasisStereoPlanarF32Core(dst []float32, left, right []float32, n int, scale, stateL, stateR, coef, verySmall float32) (float32, float32)
TEXT ·deemphasisStereoPlanarF32Core(SB), NOSPLIT, $0-112
	MOVD  dst_base+0(FP), R0
	MOVD  left_base+24(FP), R1
	MOVD  right_base+48(FP), R2
	MOVD  n+72(FP), R3

	FMOVS scale+80(FP), F0
	FMOVS stateL+84(FP), F1
	FMOVS stateR+88(FP), F2
	FMOVS coef+92(FP), F3
	FMOVS verySmall+96(FP), F4

	LSR $2, R3, R4
	AND $3, R3, R3
	CBZ R4, f32_tail_check

f32_loop4:
	FMOVS (R1), F5
	FADDS F4, F5, F5
	FMOVS (R2), F6
	FADDS F4, F6, F6

	FMOVS 4(R1), F7
	FADDS F4, F7, F7
	FMOVS 4(R2), F8
	FADDS F4, F8, F8

	FMOVS 8(R1), F9
	FADDS F4, F9, F9
	FMOVS 8(R2), F10
	FADDS F4, F10, F10

	FMOVS 12(R1), F11
	FADDS F4, F11, F11
	FMOVS 12(R2), F12
	FADDS F4, F12, F12

	FADDS F1, F5, F5
	FADDS F2, F6, F6
	FMULS F0, F5, F13
	FMULS F0, F6, F14
	FMOVS F13, (R0)
	FMOVS F14, 4(R0)

	FMADDS F3, F7, F5, F7
	FMADDS F3, F8, F6, F8
	FMULS  F0, F7, F13
	FMULS  F0, F8, F14
	FMOVS  F13, 8(R0)
	FMOVS  F14, 12(R0)

	FMADDS F3, F9, F7, F9
	FMADDS F3, F10, F8, F10
	FMULS  F0, F9, F13
	FMULS  F0, F10, F14
	FMOVS  F13, 16(R0)
	FMOVS  F14, 20(R0)

	FMADDS F3, F11, F9, F11
	FMADDS F3, F12, F10, F12
	FMULS  F0, F11, F13
	FMULS  F0, F12, F14
	FMOVS  F13, 24(R0)
	FMOVS  F14, 28(R0)

	FMULS F3, F11, F1
	FMULS F3, F12, F2

	ADD $16, R1
	ADD $16, R2
	ADD $32, R0
	SUBS $1, R4
	BNE  f32_loop4

f32_tail_check:
	CBZ R3, f32_done

f32_tail:
	FMOVS (R1), F5
	FADDS F4, F5, F5
	FADDS F1, F5, F5
	FMOVS (R2), F6
	FADDS F4, F6, F6
	FADDS F2, F6, F6

	FMULS F3, F5, F1
	FMULS F3, F6, F2
	FMULS F0, F5, F7
	FMULS F0, F6, F8
	FMOVS F7, (R0)
	FMOVS F8, 4(R0)

	ADD $4, R1
	ADD $4, R2
	ADD $8, R0
	SUBS $1, R3
	BNE  f32_tail

f32_done:
	FMOVS F1, ret+104(FP)
	FMOVS F2, ret1+108(FP)
	RET
