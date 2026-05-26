//go:build amd64 && !purego

#include "textflag.h"

// func x86RcpApprox32(x float32) float32
TEXT ·x86RcpApprox32(SB), NOSPLIT, $0-12
	MOVSS x+0(FP), X0
	RCPSS X0, X0
	MOVSS X0, ret+8(FP)
	RET

// func x86RsqrtApprox32(x float32) float32
TEXT ·x86RsqrtApprox32(SB), NOSPLIT, $0-12
	MOVSS   x+0(FP), X0
	RSQRTSS X0, X0
	MOVSS   X0, ret+8(FP)
	RET
