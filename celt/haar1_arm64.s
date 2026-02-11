#include "textflag.h"

// float32 1/sqrt(2) = 0x3F3504F3
DATA invSqrt2F32<>+0(SB)/4, $0x3F3504F3
GLOBL invSqrt2F32<>(SB), RODATA|NOPTR, $4

// func haar1Stride1Asm(x []float64, n0 int)
//
// Applies the Haar butterfly to n0 consecutive pairs of float64 values:
//   tmp1 = invSqrt2_f32 * float32(x[2*j])
//   tmp2 = invSqrt2_f32 * float32(x[2*j+1])
//   x[2*j]   = float64(tmp1 + tmp2)
//   x[2*j+1] = float64(tmp1 - tmp2)
//
// Frame layout (ABI0):
//   x_base+0(FP)   *float64
//   x_len+8(FP)    int
//   x_cap+16(FP)   int
//   n0+24(FP)      int
//
// Register allocation:
//   R0  = x pointer (advances by 32 per 2 pairs)
//   R1  = remaining pair count
//   F16 = invSqrt2 constant (float32)
//   F0-F5 = scratch for pair processing
TEXT ·haar1Stride1Asm(SB), NOSPLIT|NOFRAME, $0-32
	MOVD	n0+24(FP), R1
	CMP	$1, R1
	BLT	haar1s1_done

	MOVD	x_base+0(FP), R0
	FMOVS	invSqrt2F32<>(SB), F16	// F16 = invSqrt2 (float32)

	CMP	$2, R1
	BLT	haar1s1_tail

haar1s1_loop2:
	// --- Pair 0 ---
	FMOVD	(R0), F0	// x[2*j] as float64
	FMOVD	8(R0), F1	// x[2*j+1] as float64
	FCVTDS	F0, F2		// narrow to float32
	FCVTDS	F1, F3		// narrow to float32
	FMULS	F16, F2, F2	// tmp1 = invSqrt2 * float32(x[2*j])
	FMULS	F16, F3, F3	// tmp2 = invSqrt2 * float32(x[2*j+1])
	FADDS	F3, F2, F4	// tmp1 + tmp2
	FSUBS	F3, F2, F5	// tmp1 - tmp2
	FCVTSD	F4, F0		// widen to float64
	FCVTSD	F5, F1		// widen to float64
	FMOVD	F0, (R0)
	FMOVD	F1, 8(R0)

	// --- Pair 1 ---
	FMOVD	16(R0), F0
	FMOVD	24(R0), F1
	FCVTDS	F0, F2
	FCVTDS	F1, F3
	FMULS	F16, F2, F2
	FMULS	F16, F3, F3
	FADDS	F3, F2, F4
	FSUBS	F3, F2, F5
	FCVTSD	F4, F0
	FCVTSD	F5, F1
	FMOVD	F0, 16(R0)
	FMOVD	F1, 24(R0)

	ADD	$32, R0		// advance pointer by 4 float64 = 32 bytes
	SUB	$2, R1
	CMP	$2, R1
	BGE	haar1s1_loop2

haar1s1_tail:
	CBZ	R1, haar1s1_done

	FMOVD	(R0), F0
	FMOVD	8(R0), F1
	FCVTDS	F0, F2
	FCVTDS	F1, F3
	FMULS	F16, F2, F2
	FMULS	F16, F3, F3
	FADDS	F3, F2, F4
	FSUBS	F3, F2, F5
	FCVTSD	F4, F0
	FCVTSD	F5, F1
	FMOVD	F0, (R0)
	FMOVD	F1, 8(R0)

haar1s1_done:
	RET
