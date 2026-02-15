#include "textflag.h"

TEXT ·warpedARFeedback24(SB), NOSPLIT, $0-64
	MOVD    $11111, R0
	MOVW    R0, ret+48(FP)
	RET

TEXT ·warpedARFeedback16(SB), NOSPLIT, $0-64
	MOVD    $22222, R0
	MOVW    R0, ret+48(FP)
	RET
