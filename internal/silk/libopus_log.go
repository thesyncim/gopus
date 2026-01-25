package silk

func silkLog2Lin(inLogQ7 int32) int32 {
	if inLogQ7 < 0 {
		return 0
	}
	if inLogQ7 >= 3967 {
		return 0x7fffffff
	}

	out := int32(1) << (inLogQ7 >> 7)
	fracQ7 := inLogQ7 & 0x7f
	interp := silkSMLAWB(fracQ7, silkSMULBB(fracQ7, 128-fracQ7), -174)
	if inLogQ7 < 2048 {
		out = silkADD_RSHIFT32(out, silkMUL(out, interp), 7)
	} else {
		out = silkMLA(out, out>>7, interp)
	}
	return out
}

func silkLin2Log(inLin int32) int32 {
	if inLin <= 0 {
		return 0
	}
	lz, fracQ7 := silkCLZ_FRAC(inLin)
	return silkADD_LSHIFT32(silkSMLAWB(fracQ7, silkMUL(fracQ7, 128-fracQ7), 179), 31-lz, 7)
}
