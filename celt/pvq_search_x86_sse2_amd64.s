//go:build amd64 && !purego

#include "textflag.h"

// func x86RcpApprox4(dst, src *[4]float32)
TEXT ·x86RcpApprox4(SB), NOSPLIT, $0-16
	MOVQ  dst+0(FP), AX
	MOVQ  src+8(FP), BX
	MOVUPS (BX), X0
	RCPPS X0, X0
	MOVUPS X0, (AX)
	RET

// func x86RsqrtApprox4(dst, src *[4]float32)
TEXT ·x86RsqrtApprox4(SB), NOSPLIT, $0-16
	MOVQ   dst+0(FP), AX
	MOVQ   src+8(FP), BX
	MOVUPS (BX), X0
	RSQRTPS X0, X0
	MOVUPS X0, (AX)
	RET
