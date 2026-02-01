//go:build arm64 && !purego

#include "textflag.h"

// func kfBfly2M1(fout []kissCpx, n int)
// Radix-2 butterfly with twiddle factor = 1
// For each pair [a, b]: computes [a+b, a-b]
// Each complex is 8 bytes (2x float32)
TEXT ·kfBfly2M1(SB), NOSPLIT, $0-32
	MOVD	fout_base+0(FP), R0
	MOVD	n+24(FP), R1
	CBZ	R1, done

loop:
	// Load one butterfly: a = (R0), b = (R0+8)
	FMOVS	(R0), F0        // a.r
	FMOVS	4(R0), F1       // a.i
	FMOVS	8(R0), F2       // b.r
	FMOVS	12(R0), F3      // b.i

	// Compute sum = a + b
	FADDS	F2, F0, F4      // sum.r = a.r + b.r
	FADDS	F3, F1, F5      // sum.i = a.i + b.i

	// Compute diff = a - b
	FSUBS	F2, F0, F6      // diff.r = a.r - b.r
	FSUBS	F3, F1, F7      // diff.i = a.i - b.i

	// Store [sum, diff]
	FMOVS	F4, (R0)        // fout[0].r = sum.r
	FMOVS	F5, 4(R0)       // fout[0].i = sum.i
	FMOVS	F6, 8(R0)       // fout[1].r = diff.r
	FMOVS	F7, 12(R0)      // fout[1].i = diff.i

	ADD	$16, R0, R0     // Advance to next butterfly
	SUBS	$1, R1, R1      // Decrement counter
	BNE	loop

done:
	RET
