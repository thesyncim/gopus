#include "textflag.h"

// WORD-encoded instructions not supported by Go assembler:
//   FCVTN  Vd.2S, Vn.D2  = 0x0E616800 | (Vn<<5) | Vd   (narrow float64→float32, low half)
//   FCVTN2 Vd.4S, Vn.D2  = 0x4E616800 | (Vn<<5) | Vd   (narrow float64→float32, high half)
//   FCVTL  Vd.2D, Vn.2S  = 0x0E617800 | (Vn<<5) | Vd   (widen float32→float64, low half)
//   FCVTL2 Vd.2D, Vn.4S  = 0x4E617800 | (Vn<<5) | Vd   (widen float32→float64, high half)

// func roundFloat64ToFloat32(x []float64)
//
// Rounds each float64 element to float32 precision and back.
// Processes 4 elements per iteration using NEON FCVTN/FCVTL narrowing+widening.
TEXT ·roundFloat64ToFloat32(SB), NOSPLIT, $0-24
	MOVD x_base+0(FP), R0
	MOVD x_len+8(FP), R1

	CBZ  R1, rf_done

	// R2 = number of 4-element iterations
	LSR  $2, R1, R2
	CBZ  R2, rf_tail

rf_loop4:
	// Load 4 float64 (32 bytes)
	VLD1 (R0), [V0.D2, V1.D2]

	// Narrow: 4×float64 → 4×float32 in V4.4S
	WORD $0x0E616804              // FCVTN  V4.2S, V0.D2
	WORD $0x4E616824              // FCVTN2 V4.4S, V1.D2

	// Widen: 4×float32 → 4×float64 in V0.2D, V1.2D
	WORD $0x0E617880              // FCVTL  V0.2D, V4.2S
	WORD $0x4E617881              // FCVTL2 V1.2D, V4.4S

	// Store 4 float64
	VST1 [V0.D2, V1.D2], (R0)
	ADD  $32, R0

	SUBS $1, R2
	BNE  rf_loop4

rf_tail:
	// Handle remaining 0-3 elements
	AND  $3, R1, R2
	CBZ  R2, rf_done

rf_tail_loop:
	FMOVD  (R0), F0              // load float64
	FCVTDS F0, F1                // float64 → float32
	FCVTSD F1, F0                // float32 → float64
	FMOVD  F0, (R0)             // store float64
	ADD    $8, R0
	SUBS   $1, R2
	BNE    rf_tail_loop

rf_done:
	RET
