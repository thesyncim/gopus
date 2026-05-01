//go:build arm64

#include "textflag.h"

// func kfBfly3InnerCOrder(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)
//
// Radix-3 butterfly for the nfft=120 C-order path. This intentionally uses
// the same rounded-first-product FMA order emitted for kissMul* C-order helpers.
TEXT ·kfBfly3InnerCOrder(SB), NOSPLIT, $0-80
	MOVD fout_base+0(FP), R0
	MOVD w_base+24(FP), R1
	MOVD m+48(FP), R2
	MOVD N+56(FP), R3
	MOVD mm+64(FP), R4
	MOVD fstride+72(FP), R5

	MUL  R5, R2, R6
	LSL  $3, R6, R6
	ADD  R1, R6, R6
	FMOVS 4(R6), F24

	LSL  $3, R2, R9
	LSL  $3, R5, R11
	LSL  $1, R11, R12
	LSL  $3, R4, R4

	FMOVS $0.5, F25
	MOVD  ZR, R6

bfly3_c_outer:
	CMP  R3, R6
	BGE  bfly3_c_done

	MUL  R6, R4, R8
	ADD  R0, R8, R8
	MOVD ZR, R10
	MOVD R2, R7

bfly3_c_inner:
	FMOVS (R8), F0
	FMOVS 4(R8), F1

	ADD  R9, R8, R15
	FMOVS (R15), F2
	FMOVS 4(R15), F3

	ADD  R1, R10, R16
	FMOVS (R16), F28
	FMOVS 4(R16), F29

	FMULS F28, F2, F30
	FMSUBS F29, F30, F3, F30
	FMULS F29, F2, F31
	FMADDS F28, F31, F3, F31

	ADD  R9, R15, R15
	FMOVS (R15), F4
	FMOVS 4(R15), F5

	LSL  $1, R10, R16
	ADD  R1, R16, R16
	FMOVS (R16), F28
	FMOVS 4(R16), F29

	FMULS F28, F4, F16
	FMSUBS F29, F16, F5, F16
	FMULS F29, F4, F17
	FMADDS F28, F17, F5, F17

	FADDS F16, F30, F2
	FADDS F17, F31, F3
	FSUBS F16, F30, F4
	FSUBS F17, F31, F5

	FMULS F25, F2, F6
	FSUBS F6, F0, F6
	FMULS F25, F3, F7
	FSUBS F7, F1, F7

	FMULS F24, F4, F4
	FMULS F24, F5, F5

	FADDS F2, F0, F16
	FADDS F3, F1, F17
	FMOVS F16, (R8)
	FMOVS F17, 4(R8)

	FADDS F5, F6, F16
	FSUBS F4, F7, F17
	ADD   R9, R8, R15
	ADD   R9, R15, R16
	FMOVS F16, (R16)
	FMOVS F17, 4(R16)

	FSUBS F5, F6, F16
	FADDS F4, F7, F17
	FMOVS F16, (R15)
	FMOVS F17, 4(R15)

	ADD  $8, R8, R8
	ADD  R11, R10, R10
	SUBS $1, R7, R7
	BNE  bfly3_c_inner

	ADD $1, R6, R6
	B   bfly3_c_outer

bfly3_c_done:
	RET
