//go:build arm64 && !race

#include "textflag.h"

// func haar1Stride1Asm(x []float64, n0 int)
//
// Applies the float32-precision Haar butterfly to consecutive pairs. The math
// order matches the Go fallback exactly; the only difference is that the loop
// runs in arm64 assembly with explicit float32 conversions.
TEXT ·haar1Stride1Asm(SB), NOSPLIT, $0-32
	MOVD x_base+0(FP), R0
	MOVD n0+24(FP), R1

	CBZ  R1, stride1_done

	MOVD $0x3f3504f3, R2
	FMOVS R2, F31

	CMP  $2, R1
	BLT  stride1_tail

stride1_loop2:
	FMOVD (R0), F0
	FCVTDS F0, F0
	FMULS F31, F0, F0
	FMOVD 8(R0), F1
	FCVTDS F1, F2
	FMADDS F2, F0, F31, F3
	FMSUBS F2, F0, F31, F0
	FCVTSD F3, F3
	FCVTSD F0, F0
	FMOVD F3, (R0)
	FMOVD F0, 8(R0)

	FMOVD 16(R0), F4
	FCVTDS F4, F4
	FMULS F31, F4, F4
	FMOVD 24(R0), F7
	FCVTDS F7, F5
	FMADDS F5, F4, F31, F6
	FMSUBS F5, F4, F31, F4
	FCVTSD F6, F6
	FCVTSD F4, F4
	FMOVD F6, 16(R0)
	FMOVD F4, 24(R0)

	ADD  $32, R0
	SUBS $2, R1, R1
	CMP  $2, R1
	BGE  stride1_loop2

stride1_tail:
	CBZ  R1, stride1_done

	FMOVD (R0), F0
	FCVTDS F0, F0
	FMULS F31, F0, F0
	FMOVD 8(R0), F1
	FCVTDS F1, F2
	FMADDS F2, F0, F31, F3
	FMSUBS F2, F0, F31, F0
	FCVTSD F3, F3
	FCVTSD F0, F0
	FMOVD F3, (R0)
	FMOVD F0, 8(R0)

stride1_done:
	RET

// func haar1Stride2Asm(x []float64, n0 int)
//
// Applies the float32-precision Haar butterfly to stride-2 blocks
// [x0 x1 x2 x3] -> [a0+b0 a1+b1 a0-b0 a1-b1].
TEXT ·haar1Stride2Asm(SB), NOSPLIT, $0-32
	MOVD x_base+0(FP), R0
	MOVD n0+24(FP), R1

	CBZ  R1, stride2_done

	MOVD $0x3f3504f3, R2
	FMOVS R2, F31

	CMP  $2, R1
	BLT  stride2_tail

stride2_loop2:
	FMOVD (R0), F0
	FCVTDS F0, F0
	FMULS F31, F0, F0
	FMOVD 16(R0), F1
	FCVTDS F1, F2
	FMADDS F2, F0, F31, F4
	FMSUBS F2, F0, F31, F5
	FCVTSD F4, F4
	FCVTSD F5, F5

	FMOVD 8(R0), F6
	FCVTDS F6, F6
	FMULS F31, F6, F6
	FMOVD 24(R0), F7
	FCVTDS F7, F7
	FMADDS F7, F6, F31, F10
	FMSUBS F7, F6, F31, F11
	FCVTSD F10, F10
	FCVTSD F11, F11

	FMOVD F4, (R0)
	FMOVD F10, 8(R0)
	FMOVD F5, 16(R0)
	FMOVD F11, 24(R0)

	FMOVD 32(R0), F12
	FCVTDS F12, F12
	FMULS F31, F12, F12
	FMOVD 48(R0), F13
	FCVTDS F13, F13
	FMADDS F13, F12, F31, F16
	FMSUBS F13, F12, F31, F17
	FCVTSD F16, F16
	FCVTSD F17, F17

	FMOVD 40(R0), F18
	FCVTDS F18, F18
	FMULS F31, F18, F18
	FMOVD 56(R0), F19
	FCVTDS F19, F19
	FMADDS F19, F18, F31, F22
	FMSUBS F19, F18, F31, F23
	FCVTSD F22, F22
	FCVTSD F23, F23

	FMOVD F16, 32(R0)
	FMOVD F22, 40(R0)
	FMOVD F17, 48(R0)
	FMOVD F23, 56(R0)

	ADD  $64, R0
	SUBS $2, R1, R1
	CMP  $2, R1
	BGE  stride2_loop2

stride2_tail:
	CBZ  R1, stride2_done

	FMOVD (R0), F0
	FCVTDS F0, F0
	FMULS F31, F0, F0
	FMOVD 16(R0), F1
	FCVTDS F1, F2
	FMADDS F2, F0, F31, F4
	FMSUBS F2, F0, F31, F5
	FCVTSD F4, F4
	FCVTSD F5, F5

	FMOVD 8(R0), F6
	FCVTDS F6, F6
	FMULS F31, F6, F6
	FMOVD 24(R0), F7
	FCVTDS F7, F7
	FMADDS F7, F6, F31, F10
	FMSUBS F7, F6, F31, F11
	FCVTSD F10, F10
	FCVTSD F11, F11

	FMOVD F4, (R0)
	FMOVD F10, 8(R0)
	FMOVD F5, 16(R0)
	FMOVD F11, 24(R0)

stride2_done:
	RET
