//go:build amd64 && !purego

#include "textflag.h"

// func pitchFMADD32AVXFMA(a, b, c float32) float32
TEXT ·pitchFMADD32AVXFMA(SB), NOSPLIT, $0-20
	VMOVSS a+0(FP), X0
	VMOVSS b+4(FP), X1
	VMOVSS c+8(FP), X2
	VFMADD231SS X0, X1, X2
	VMOVSS X2, ret+16(FP)
	RET
