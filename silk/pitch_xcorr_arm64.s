#include "textflag.h"

// func celtPitchXcorrFloatImpl(x, y []float32, out []float32, length, maxPitch int)
TEXT ·celtPitchXcorrFloatImpl(SB), NOSPLIT, $0-80
	MOVD    x_base+0(FP), R0
	MOVD    y_base+24(FP), R1
	MOVD    out_base+48(FP), R2
	MOVD    length+72(FP), R3
	MOVD    maxPitch+80(FP), R4

	CMP     $0, R4
	BLE     done
	CMP     $0, R3
	BLE     done

	MOVD    ZR, R5          // lag = 0

outer_loop:
	ADD     $4, R5, R10
	CMP     R10, R4
	BLT     outer_tail

	VEOR    V0.B16, V0.B16, V0.B16

	MOVD    ZR, R6          // k = 0
	
	MOVD    R0, R7          // x_ptr = x
	// y_ptr = y + lag*4
	LSL     $2, R5, R11
	ADD     R1, R11, R8

inner_loop4:
	SUB     R6, R3, R12     // remaining = length - k
	CMP     $4, R12
	BLT     inner_tail

	// Load x[k...k+3]
	VLD1.P  16(R7), [V4.B16]

	// Load y window: y[lag+k ... lag+k+6]
	VLD1.P  16(R8), [V5.B16]
	VLD1    (R8), [V6.B16]

	// FMLA V0.4S, V5.4S, V4.S[0]
	WORD    $0x4f8410a0

	// EXT V1.16B, V5.16B, V6.16B, $4
	WORD    $0x6e0620a1
	// FMLA V0.4S, V1.4S, V4.S[1]
	WORD    $0x4fa41020

	// EXT V2.16B, V5.16B, V6.16B, $8
	WORD    $0x6e0640a2
	// FMLA V0.4S, V2.4S, V4.S[2]
	WORD    $0x4f841840

	// EXT V3.16B, V5.16B, V6.16B, $12
	WORD    $0x6e0660a3
	// FMLA V0.4S, V3.4S, V4.S[3]
	WORD    $0x4fa41860

	ADD     $4, R6
	B       inner_loop4

inner_tail:
	CMP     R6, R3
	BEQ     store_block

	FMOVS   (R7), F4        // x[k]
	ADD     $4, R7
	
	// Load y[lag+k ... lag+k+3]
	VLD1    (R8), [V5.B16]
	ADD     $4, R8

	// FMLA V0.4S, V5.4S, V4.S[0]
	WORD    $0x4f8410a0
	
	ADD     $1, R6
	B       inner_tail

store_block:
	VST1.P  [V0.B16], 16(R2)
	ADD     $4, R5
	B       outer_loop

outer_tail:
	CMP     R5, R4
	BEQ     done

	MOVD    ZR, R6          // k = 0
	FMOVS   ZR, F0          // acc = 0
	
	MOVD    R0, R7          // x_ptr
	// y_ptr = y + lag*4
	LSL     $2, R5, R11
	ADD     R1, R11, R8

single_lag_loop:
	CMP     R6, R3
	BEQ     single_lag_store

	FMOVS   (R7), F1
	ADD     $4, R7
	FMOVS   (R8), F2
	ADD     $4, R8
	
	FMULS   F1, F2, F3
	FADDS   F3, F0, F0
	ADD     $1, R6
	B       single_lag_loop

single_lag_store:
	FMOVS   F0, (R2)
	ADD     $4, R2
	ADD     $1, R5
	B       outer_tail

done:
	RET
