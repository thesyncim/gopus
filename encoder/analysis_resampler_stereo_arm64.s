#include "textflag.h"

// func silkResamplerDown2HPStereoImpl(state, out, in []float32, scale float32, n int) float32
//
// Exact arm64 implementation of the 48 kHz stereo analysis down2/high-pass
// helper. It preserves the same float32 operation order as the Go reference and
// accumulates hp energy in float64 after a rounded float32 square, matching:
//   hpEner += float64(out32HP * out32HP)
TEXT ·silkResamplerDown2HPStereoImpl(SB), NOSPLIT, $0-96
	MOVD  state_base+0(FP), R0
	MOVD  out_base+24(FP), R1
	MOVD  in_base+48(FP), R2
	FMOVS scale+72(FP), F3
	MOVD  n+80(FP), R3

	FMOVS (R0), F0
	FMOVS 4(R0), F1
	FMOVS 8(R0), F2

	MOVD $0x3f1b80ff, R4
	FMOVS R4, F4              // coef0 = 0.6074371
	MOVD $0x3e1a3ec0, R5
	FMOVS R5, F5              // coef1 = 0.15063
	MOVD $0x3f000000, R6
	FMOVS R6, F6              // 0.5
	FMOVD ZR, F20             // hpEner accumulator (float64)

	CMP   $2, R3
	BLT   stereo_tail

stereo_loop2:
	// mixed0 := (in[0] + in[1]) * scale
	FMOVS (R2), F7
	FMOVS 4(R2), F8
	FADDS F8, F7, F7
	// mixed1 := (in[2] + in[3]) * scale
	FMOVS 8(R2), F8
	FMOVS 12(R2), F9
	FADDS F9, F8, F8

	// First output sample.
	FNMSUBS F7, F0, F3, F9    // y0 = mixed0 - s0
	FMULS   F9, F4, F10       // xf0 = coef0 * y0
	FMADDS  F4, F0, F9, F11   // out32HP = s0 + xf0
	FMADDS  F7, F10, F3, F0   // s0 = mixed0 + xf0

	FNMSUBS F8, F1, F3, F9    // y1 = mixed1 - s1
	FMULS   F9, F5, F10       // xf1 = coef1 * y1
	FADDS   F11, F1, F12      // tmp = out32HP + s1
	FMADDS  F5, F12, F9, F12  // out32 = tmp + xf1
	FNMADDS F8, F2, F3, F14   // y2 = -mixed1 - s2
	FNMULS  F3, F8, F13       // negMixed1 = -mixed1
	FADDS   F11, F2, F11      // tmpHP = out32HP + s2
	FMADDS  F5, F11, F14, F11 // out32HP = tmpHP + coef1*y2
	FMADDS  F5, F13, F14, F2  // s2 = negMixed1 + coef1*y2
	FMADDS  F8, F10, F3, F1   // s1 = mixed1 + xf1

	FMULS  F11, F11, F15
	FCVTSD F15, F14
	FADDD  F14, F20, F20
	FMULS  F6, F12, F15
	FMOVS  F15, (R1)

	// mixed0 := (in[4] + in[5]) * scale
	FMOVS 16(R2), F7
	FMOVS 20(R2), F8
	FADDS F8, F7, F7
	// mixed1 := (in[6] + in[7]) * scale
	FMOVS 24(R2), F8
	FMOVS 28(R2), F9
	FADDS F9, F8, F8

	// Second output sample.
	FNMSUBS F7, F0, F3, F9
	FMULS   F9, F4, F10
	FMADDS  F4, F0, F9, F11
	FMADDS  F7, F10, F3, F0

	FNMSUBS F8, F1, F3, F9
	FMULS   F9, F5, F10
	FADDS   F11, F1, F12
	FMADDS  F5, F12, F9, F12
	FNMADDS F8, F2, F3, F14
	FNMULS  F3, F8, F13
	FADDS   F11, F2, F11
	FMADDS  F5, F11, F14, F11
	FMADDS  F5, F13, F14, F2
	FMADDS  F8, F10, F3, F1

	FMULS  F11, F11, F15
	FCVTSD F15, F14
	FADDD  F14, F20, F20
	FMULS  F6, F12, F15
	FMOVS  F15, 4(R1)

	ADD   $32, R2
	ADD   $8, R1
	SUBS  $2, R3, R3
	CMP   $2, R3
	BGE   stereo_loop2

stereo_tail:
	CBZ   R3, stereo_done

	FMOVS (R2), F7
	FMOVS 4(R2), F8
	FADDS F8, F7, F7

	FMOVS 8(R2), F8
	FMOVS 12(R2), F9
	FADDS F9, F8, F8

	FNMSUBS F7, F0, F3, F9
	FMULS   F9, F4, F10
	FMADDS  F4, F0, F9, F11
	FMADDS  F7, F10, F3, F0

	FNMSUBS F8, F1, F3, F9
	FMULS   F9, F5, F10
	FADDS   F11, F1, F12
	FMADDS  F5, F12, F9, F12
	FNMADDS F8, F2, F3, F14
	FNMULS  F3, F8, F13
	FADDS   F11, F2, F11
	FMADDS  F5, F11, F14, F11
	FMADDS  F5, F13, F14, F2
	FMADDS  F8, F10, F3, F1

	FMULS  F11, F11, F15
	FCVTSD F15, F14
	FADDD  F14, F20, F20
	FMULS  F6, F12, F15
	FMOVS  F15, (R1)

stereo_done:
	FMOVS F0, (R0)
	FMOVS F1, 4(R0)
	FMOVS F2, 8(R0)
	FCVTDS F20, F0
	FMOVS  F0, ret+88(FP)
	RET
