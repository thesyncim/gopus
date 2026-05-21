//go:build arm64 && !purego

#include "textflag.h"

// func fma32(a, b, c float32) float32
TEXT ·fma32(SB), NOSPLIT, $0-20
	FMOVS a+0(FP), F0
	FMOVS b+4(FP), F1
	FMOVS c+8(FP), F2
	FMADDS F0, F2, F1, F0
	FMOVS F0, ret+16(FP)
	RET
