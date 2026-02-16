#include "textflag.h"

// WORD-encoded instructions:
//   FCVTN  Vd.2S, Vn.D2          = 0x0E616800 | (Vn<<5) | Vd
//   FCVTN2 Vd.4S, Vn.D2          = 0x4E616800 | (Vn<<5) | Vd
//   FMUL   Vd.4S, Vn.4S, Vm.4S   = 0x6E20DC00 | (Vm<<16) | (Vn<<5) | Vd
//   FADDP  Vd.4S, Vn.4S, Vm.4S   = 0x6E20D400 | (Vm<<16) | (Vn<<5) | Vd

// func transientEnergyPairs(tmp []float64, x2out []float32, len2 int) float64
//
// Computes energy of sample pairs: x2[i] = float32(tmp[2*i])^2 + float32(tmp[2*i+1])^2
// Returns sum of all x2 values as float64 (containing float32 value).
//
// Register allocation:
//   R0  = tmp pointer (advances)
//   R1  = x2out pointer (advances)
//   R2  = remaining count
//   F16 = scalar mean accumulator (float32)
//   V0-V7 = temporaries
TEXT ·transientEnergyPairs(SB), NOSPLIT, $0-64
	MOVD  tmp_base+0(FP), R0
	MOVD  x2out_base+24(FP), R1
	MOVD  len2+48(FP), R2

	// Zero accumulator
	FMOVS ZR, F16

	CBZ   R2, te_done

	// Main loop: 2 pairs per iteration (4 float64 → 4 float32 → 2 x2 values)
	CMP   $2, R2
	BLT   te_scalar

	MOVD  $32, R3              // post-increment for 4 float64 = 32 bytes

te_loop2:
	// Load 4 float64 (2 pairs): tmp[2*i], tmp[2*i+1], tmp[2*(i+1)], tmp[2*(i+1)+1]
	VLD1.P (R0)(R3), [V0.D2, V1.D2]

	// Convert 4 float64 → 4 float32
	WORD $0x0E616804                  // FCVTN  V4.2S, V0.D2
	WORD $0x4E616824                  // FCVTN2 V4.4S, V1.D2
	// V4 = [f32(tmp[2i]), f32(tmp[2i+1]), f32(tmp[2i+2]), f32(tmp[2i+3])]

	// Square all 4 values (separate MUL, not FMA — matches Go rounding)
	// FMUL V5.4S, V4.4S, V4.4S
	WORD $0x6E24DC85                  // V5 = [t0^2, t1^2, t2^2, t3^2]

	// Pairwise add: x2[i] = t0^2 + t1^2, x2[i+1] = t2^2 + t3^2
	WORD $0x6E25D4A5                  // FADDP V5.4S, V5.4S, V5.4S
	// V5 = [x2_0, x2_1, x2_0, x2_1]

	// Store 2 x2 values (low 64 bits = 2 consecutive float32)
	FMOVD F5, (R1)
	ADD   $8, R1

	// Scalar accumulate mean in order: mean += x2[i], mean += x2[i+1]
	FADDS F5, F16, F16                // mean += x2[i] (V5.S[0])
	VEXT  $4, V5.B16, V5.B16, V6.B16 // V6.S[0] = x2[i+1]
	FADDS F6, F16, F16                // mean += x2[i+1]

	SUB   $2, R2
	CMP   $2, R2
	BGE   te_loop2

te_scalar:
	// Handle 1 remaining pair
	CBZ   R2, te_done

	FMOVD (R0), F0                    // tmp[2*i]
	FMOVD 8(R0), F1                   // tmp[2*i+1]
	FCVTDS F0, F2                     // float32(tmp[2*i])
	FCVTDS F1, F3                     // float32(tmp[2*i+1])
	FMULS  F2, F2, F4                // t0*t0
	FMULS  F3, F3, F5                // t1*t1
	FADDS  F5, F4, F4                // x2 = t0*t0 + t1*t1
	FMOVS  F4, (R1)                  // store x2out[i]
	FADDS  F4, F16, F16              // mean += x2

te_done:
	FCVTSD F16, F16
	FMOVD  F16, ret+56(FP)
	RET
