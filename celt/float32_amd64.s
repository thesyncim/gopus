#include "textflag.h"

// func roundFloat64ToFloat32(x []float64)
//
// Rounds each float64 element to float32 precision and back.
// Processes 4 elements per iteration using AVX CVTPD2PS/CVTPS2PD round-trip.
TEXT ·roundFloat64ToFloat32(SB), NOSPLIT, $0-24
	MOVQ x_base+0(FP), AX
	MOVQ x_len+8(FP), CX

	TESTQ CX, CX
	JZ    rf_done

	// DX = number of 4-element iterations
	MOVQ  CX, DX
	SHRQ  $2, DX
	TESTQ DX, DX
	JZ    rf_tail

rf_loop4:
	// Load 4 float64 (32 bytes) and round-trip through float32
	// Batch A: first 2 float64
	VMOVUPD   (AX), X0
	VCVTPD2PSX X0, X1            // 2×f64 → 2×f32 (low 64 bits)
	VCVTPS2PD X1, X0             // 2×f32 → 2×f64
	VMOVUPD   X0, (AX)

	// Batch B: next 2 float64
	VMOVUPD   16(AX), X2
	VCVTPD2PSX X2, X3
	VCVTPS2PD X3, X2
	VMOVUPD   X2, 16(AX)

	ADDQ $32, AX
	DECQ DX
	JNZ  rf_loop4

rf_tail:
	ANDQ  $3, CX
	TESTQ CX, CX
	JZ    rf_done

rf_tail_loop:
	VMOVSD    (AX), X0
	VCVTSD2SS X0, X0, X0
	VCVTSS2SD X0, X0, X0
	VMOVSD    X0, (AX)
	ADDQ      $8, AX
	DECQ      CX
	JNZ       rf_tail_loop

rf_done:
	VZEROUPPER
	RET
