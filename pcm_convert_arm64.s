#include "textflag.h"

// func convertFloat32ToInt16Unit(dst []int16, src []float32, n int) bool
//
// Converts the common already-soft-clipped path where every sample is in
// [-1, 1]. Returns false on the first out-of-range or NaN sample so the Go
// soft-clip fallback can process the whole frame.
TEXT ·convertFloat32ToInt16Unit(SB), NOSPLIT, $0-64
	MOVD  dst_base+0(FP), R0
	MOVD  src_base+24(FP), R1
	MOVD  n+48(FP), R2

	CBZ   R2, convert_done
	FMOVS $1.0, F1
	FMOVS $32768.0, F2
	MOVD  ZR, R3
	MOVD  $32767, R5

convert_loop:
	FMOVS (R1)(R3<<2), F0
	FABSS F0, F3
	FCMPS F1, F3
	BVS   convert_fallback
	BGT   convert_fallback

	FMULS F2, F0, F0
	WORD  $0x1e200004              // FCVTNS W4, S0
	CMPW  R5, R4
	BLE   convert_store
	MOVW  R5, R4

convert_store:
	MOVH  R4, (R0)(R3<<1)
	ADD   $1, R3
	CMP   R2, R3
	BLT   convert_loop

convert_done:
	MOVD  $1, R6
	MOVB  R6, ret+56(FP)
	RET

convert_fallback:
	MOVB  ZR, ret+56(FP)
	RET
