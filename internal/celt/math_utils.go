package celt

func celtUdiv(n, d int) int {
	if d <= 0 {
		return 0
	}
	if n < 0 {
		n = 0
	}
	return n / d
}

func celtSudiv(n, d int) int {
	if d <= 0 {
		return 0
	}
	if n < 0 {
		return -celtUdiv(-n, d)
	}
	return celtUdiv(n, d)
}

func fracMul16(a, b int) int {
	return int((16384 + int32(int16(a))*int32(int16(b))) >> 15)
}

func bitexactCos(x int) int {
	tmp := (4096 + int32(x)*int32(x)) >> 13
	x2 := int(tmp)
	x2 = (32767 - x2) + fracMul16(x2, (-7651+fracMul16(x2, (8277+fracMul16(-626, x2)))))
	return 1 + x2
}

func bitexactLog2tan(isin, icos int) int {
	lc := ilog32(uint32(icos))
	ls := ilog32(uint32(isin))
	icos <<= 15 - lc
	isin <<= 15 - ls
	return (ls-lc)*(1<<11) + fracMul16(isin, fracMul16(isin, -2597)+7932) - fracMul16(icos, fracMul16(icos, -2597)+7932)
}

// isqrt32 computes floor(sqrt(val)) with exact arithmetic.
func isqrt32(val uint32) uint32 {
	g := uint32(0)
	bshift := (ilog32(val) - 1) >> 1
	b := uint32(1) << bshift
	for bshift >= 0 {
		t := (((g << 1) + b) << bshift)
		if t <= val {
			g += b
			val -= t
		}
		b >>= 1
		bshift--
		if bshift < 0 {
			break
		}
	}
	return g
}

func ilog32(x uint32) int {
	if x == 0 {
		return 0
	}
	n := 0
	if x >= (1 << 16) {
		n += 16
		x >>= 16
	}
	if x >= (1 << 8) {
		n += 8
		x >>= 8
	}
	if x >= (1 << 4) {
		n += 4
		x >>= 4
	}
	if x >= (1 << 2) {
		n += 2
		x >>= 2
	}
	if x >= (1 << 1) {
		n += 1
		x >>= 1
	}
	return n + int(x)
}
