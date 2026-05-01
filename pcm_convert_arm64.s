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
	MOVD  $32767, R5
	MOVD  $-1, R12

	FMOVS $1.0, F3
	WORD  $0x4e040463              // DUP V3.4S, V3.S[0]
	FMOVS $32768.0, F4
	WORD  $0x4e040484              // DUP V4.4S, V4.S[0]
	VDUP  R5, V8.S4

	CMP   $8, R2
	BLT   convert_vector4_check

convert_vector8_loop:
	VLD1.P 16(R1), [V0.S4]
	WORD   $0x4ea0f801             // FABS V1.4S, V0.4S
	WORD   $0x6e21e462             // FCMGE V2.4S, V3.4S, V1.4S
	VLD1.P 16(R1), [V6.S4]
	WORD   $0x4ea0f8c7             // FABS V7.4S, V6.4S
	WORD   $0x6e27e467             // FCMGE V7.4S, V3.4S, V7.4S
	VAND   V7.B16, V2.B16, V2.B16
	VMOV   V2.D[0], R10
	VMOV   V2.D[1], R11
	CMP    R12, R10
	BNE    convert_fallback
	CMP    R12, R11
	BNE    convert_fallback

	WORD   $0x6e24dc00             // FMUL V0.4S, V0.4S, V4.4S
	WORD   $0x4e21a805             // FCVTNS V5.4S, V0.4S
	WORD   $0x4ea86ca5             // SMIN V5.4S, V5.4S, V8.4S
	WORD   $0x0e6148a9             // SQXTN V9.4H, V5.4S
	VST1.P [V9.H4], 8(R0)

	WORD   $0x6e24dcc6             // FMUL V6.4S, V6.4S, V4.4S
	WORD   $0x4e21a8c5             // FCVTNS V5.4S, V6.4S
	WORD   $0x4ea86ca5             // SMIN V5.4S, V5.4S, V8.4S
	WORD   $0x0e6148aa             // SQXTN V10.4H, V5.4S
	VST1.P [V10.H4], 8(R0)

	SUBS  $8, R2
	CMP   $8, R2
	BGE   convert_vector8_loop

convert_vector4_check:
	CMP   $4, R2
	BLT   convert_tail

convert_vector_loop:
	VLD1.P 16(R1), [V0.S4]
	WORD   $0x4ea0f801             // FABS V1.4S, V0.4S
	WORD   $0x6e21e462             // FCMGE V2.4S, V3.4S, V1.4S
	VMOV   V2.D[0], R10
	VMOV   V2.D[1], R11
	CMP    R12, R10
	BNE    convert_fallback
	CMP    R12, R11
	BNE    convert_fallback

	WORD   $0x6e24dc00             // FMUL V0.4S, V0.4S, V4.4S
	WORD   $0x4e21a805             // FCVTNS V5.4S, V0.4S
	WORD   $0x4ea86ca5             // SMIN V5.4S, V5.4S, V8.4S
	WORD   $0x0e6148a9             // SQXTN V9.4H, V5.4S
	VST1.P [V9.H4], 8(R0)

	SUBS  $4, R2
	CMP   $4, R2
	BGE   convert_vector_loop

convert_tail:
	CBZ   R2, convert_done

convert_tail_loop:
	FMOVS (R1), F0
	FABSS F0, F3
	FCMPS F1, F3
	BVS   convert_fallback
	BGT   convert_fallback
	FMULS F2, F0, F0
	WORD  $0x1e200004              // FCVTNS W4, S0
	CMPW  R5, R4
	BLE   convert_store0
	MOVW  R5, R4
convert_store0:
	MOVH  R4, (R0)
	ADD   $4, R1
	ADD   $2, R0
	SUBS  $1, R2
	BNE   convert_tail_loop

convert_done:
	MOVD  $1, R6
	MOVB  R6, ret+56(FP)
	RET

convert_fallback:
	MOVB  ZR, ret+56(FP)
	RET
