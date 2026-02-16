#include "textflag.h"

// func innerProductF32(a, b []float32, length int) float64
TEXT ·innerProductF32(SB), NOSPLIT, $0-64
	MOVD    a_base+0(FP), R0
	MOVD    b_base+24(FP), R3
	MOVD    length+48(FP), R6

	VEOR    V0.B16, V0.B16, V0.B16
	VEOR    V1.B16, V1.B16, V1.B16
	FMOVD   ZR, F20

	CMP     $0, R6
	BLE     done

loop4:
	CMP     $4, R6
	BLT     tail

	VLD1.P  16(R0), [V2.D2]
	VLD1.P  16(R3), [V5.D2]

	// FCVTL V3.2D, V2.2S
	WORD    $0x0e617843
	// FCVTL2 V4.2D, V2.4S
	WORD    $0x4e617844
	// FCVTL V6.2D, V5.2S
	WORD    $0x0e6178a6
	// FCVTL2 V7.2D, V5.4S
	WORD    $0x4e6178a7

	// FMUL V3.2D, V3.2D, V6.2D (V3 = V3 * V6)
	WORD    $0x6e66dc63
	// FMUL V4.2D, V4.2D, V7.2D (V4 = V4 * V7)
	WORD    $0x6e67dc84

	// FADD V0.2D, V0.2D, V3.2D (V0 += V3)
	WORD    $0x4e63d400
	// FADD V1.2D, V1.2D, V4.2D (V1 += V4)
	WORD    $0x4e64d421

	SUB     $4, R6
	B       loop4

tail:
	CMP     $0, R6
	BEQ     reduce

	FMOVS   (R0), F2
	FMOVS   (R3), F3
	ADD     $4, R0
	ADD     $4, R3

	FCVTSD  F2, F2
	FCVTSD  F3, F3
	FMULD   F2, F3, F4
	FADDD   F4, F20, F20

	SUB     $1, R6
	B       tail

reduce:
	WORD    $0x4e60d420 // FADD V0, V1, V0
	WORD    $0x6e60d400 // FADDP V0, V0, V0
	FADDD   F20, F0, F0

done:
	FMOVD   F0, ret+56(FP)
	RET

TEXT ·innerProductFLP(SB), NOSPLIT, $0-64
	B       ·innerProductF32(SB)

TEXT ·energyF32(SB), NOSPLIT, $0-40
	MOVD    x_base+0(FP), R0
	MOVD    length+24(FP), R6

	VEOR    V0.B16, V0.B16, V0.B16
	VEOR    V1.B16, V1.B16, V1.B16
	FMOVD   ZR, F20

	CMP     $0, R6
	BLE     done_energy

loop4_energy:
	CMP     $4, R6
	BLT     tail_energy

	VLD1.P  16(R0), [V2.D2]

	// FCVTL V3.2D, V2.2S
	WORD    $0x0e617843
	// FCVTL2 V4.2D, V2.4S
	WORD    $0x4e617844

	// FMUL V3.2D, V3.2D, V3.2D (Square)
	WORD    $0x6e63dc63
	// FMUL V4.2D, V4.2D, V4.2D
	WORD    $0x6e64dc84

	// FADD V0.2D, V0.2D, V3.2D
	WORD    $0x4e63d400
	// FADD V1.2D, V1.2D, V4.2D
	WORD    $0x4e64d421

	SUB     $4, R6
	B       loop4_energy

tail_energy:
	CMP     $0, R6
	BEQ     reduce_energy

	FMOVS   (R0), F2
	ADD     $4, R0
	FCVTSD  F2, F2
	FMULD   F2, F2, F4
	FADDD   F4, F20, F20

	SUB     $1, R6
	B       tail_energy

reduce_energy:
	WORD    $0x4e60d420
	WORD    $0x6e60d400
	FADDD   F20, F0, F0

done_energy:
	FMOVD   F0, ret+32(FP)
	RET
