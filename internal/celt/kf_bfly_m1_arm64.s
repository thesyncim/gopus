//go:build arm64 && !purego

#include "textflag.h"

// func kfBfly2M1(fout []kissCpx, n int)
TEXT ·kfBfly2M1(SB), NOSPLIT, $0-32
	MOVD	fout_base+0(FP), R0
	MOVD	n+24(FP), R1
	CBZ	R1, done

loop:
	VLD1	(R0), [V0.S4]      // r0,i0,r1,i1
	VUZP1	V0.S4, V0.S4, V1.S4
	VUZP2	V0.S4, V0.S4, V2.S4
	VREV64	V1.S4, V3.S4
	VREV64	V2.S4, V4.S4
	VADD	V1.S4, V3.S4, V5.S4
	VADD	V2.S4, V4.S4, V6.S4
	VSUB	V1.S4, V3.S4, V7.S4
	VSUB	V2.S4, V4.S4, V8.S4
	VZIP1	V5.S4, V6.S4, V9.S4
	VZIP1	V7.S4, V8.S4, V10.S4
	VZIP1	V9.D2, V10.D2, V11.D2
	VST1	[V11.S4], (R0)
	ADD	$16, R0, R0
	SUBS	$1, R1, R1
	BNE	loop

done:
	RET
