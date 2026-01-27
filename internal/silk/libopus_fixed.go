package silk

import "math/bits"

func silkAbs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

func silkAbsInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func silkMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func silkMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func silkMin32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func silkMax32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func silkLimitInt(x, min, max int) int {
	if x < min {
		return min
	}
	if x > max {
		return max
	}
	return x
}

func silkLimit32(x, min, max int32) int32 {
	if x < min {
		return min
	}
	if x > max {
		return max
	}
	return x
}

func silkRSHIFT(x int32, shift int) int32 {
	return x >> shift
}

func silkLSHIFT(x int32, shift int) int32 {
	return x << shift
}

func silkRSHIFT_ROUND(x int32, shift int) int32 {
	if shift <= 0 {
		return x
	}
	if shift == 1 {
		return (x >> 1) + (x & 1)
	}
	return ((x >> (shift - 1)) + 1) >> 1
}

func silkRSHIFT_ROUND64(x int64, shift int) int64 {
	if shift <= 0 {
		return x
	}
	if shift == 1 {
		return (x >> 1) + (x & 1)
	}
	return ((x >> (shift - 1)) + 1) >> 1
}

func silkRSHIFT64(x int64, shift int) int64 {
	return x >> shift
}

func silkADD_LSHIFT32(a int32, b int32, shift int) int32 {
	return a + (b << shift)
}

func silkADD_RSHIFT32(a int32, b int32, shift int) int32 {
	return a + (b >> shift)
}

func silkSMULWB(a, b int32) int32 {
	return int32((int64(a) * int64(int16(b))) >> 16)
}

func silkSMLAWB(a, b, c int32) int32 {
	return a + int32((int64(b)*int64(int16(c)))>>16)
}

func silkSMULBB(a, b int32) int32 {
	return int32(int16(a)) * int32(int16(b))
}

func silkSMLABB(a, b, c int32) int32 {
	return a + int32(int16(b))*int32(int16(c))
}

func silkSMULWW(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 16)
}

func silkSMLAWW(a, b, c int32) int32 {
	return a + int32((int64(b)*int64(c))>>16)
}

func silkMUL(a, b int32) int32 {
	return int32(int64(a) * int64(b))
}

func silkMLA(a, b, c int32) int32 {
	return a + b*c
}

func silkSMULL(a, b int32) int64 {
	return int64(a) * int64(b)
}

func silkSMMUL(a, b int32) int32 {
	return int32(silkRSHIFT64(silkSMULL(a, b), 32))
}

func silkSAT16(x int32) int16 {
	if x > 32767 {
		return 32767
	}
	if x < -32768 {
		return -32768
	}
	return int16(x)
}

func silkLShiftSAT32(x int32, shift int) int32 {
	v := int64(x) << shift
	if v > int64((1<<31)-1) {
		return int32((1 << 31) - 1)
	}
	if v < int64(-1<<31) {
		return int32(-1 << 31)
	}
	return int32(v)
}

func silkAddSat32(a, b int32) int32 {
	v := int64(a) + int64(b)
	if v > int64((1<<31)-1) {
		return int32((1 << 31) - 1)
	}
	if v < int64(-1<<31) {
		return int32(-1 << 31)
	}
	return int32(v)
}

func silkSubSat32(a, b int32) int32 {
	v := int64(a) - int64(b)
	if v > int64((1<<31)-1) {
		return int32((1 << 31) - 1)
	}
	if v < int64(-1<<31) {
		return int32(-1 << 31)
	}
	return int32(v)
}

func silkDiv32_16(a, b int32) int32 {
	if b == 0 {
		return 0
	}
	return a / b
}

func silkDiv32(a, b int32) int32 {
	if b == 0 {
		return 0
	}
	return a / b
}

func silkDiv32VarQ(a32, b32 int32, q int) int32 {
	if b32 == 0 {
		return 0
	}
	if q < 0 {
		q = 0
	}

	aHeadrm := int32(silkCLZ32(silkAbs32(a32)) - 1)
	a32Nrm := silkLSHIFT(a32, int(aHeadrm))
	bHeadrm := int32(silkCLZ32(silkAbs32(b32)) - 1)
	b32Nrm := silkLSHIFT(b32, int(bHeadrm))

	b32Inv := silkDiv32_16(int32(0x7fffffff>>2), int32(b32Nrm>>16))
	result := silkSMULWB(a32Nrm, b32Inv)

	a32Nrm = a32Nrm - silkLSHIFT(silkSMMUL(b32Nrm, result), 3)
	result = silkSMLAWB(result, a32Nrm, b32Inv)

	lshift := int(29 + aHeadrm - bHeadrm - int32(q))
	if lshift < 0 {
		return silkLShiftSAT32(result, -lshift)
	}
	if lshift < 32 {
		return result >> lshift
	}
	return 0
}

func silkInverse32VarQ(b32 int32, q int) int32 {
	if b32 == 0 || q <= 0 {
		return 0
	}

	bHeadrm := int32(silkCLZ32(silkAbs32(b32)) - 1)
	b32Nrm := silkLSHIFT(b32, int(bHeadrm))

	b32Inv := silkDiv32_16(int32(0x7fffffff>>2), int32(b32Nrm>>16))
	result := silkLSHIFT(b32Inv, 16)

	errQ32 := silkLSHIFT(int32((1<<29)-silkSMULWB(b32Nrm, b32Inv)), 3)
	result = silkSMLAWW(result, errQ32, b32Inv)

	lshift := int(61 - bHeadrm - int32(q))
	if lshift <= 0 {
		return silkLShiftSAT32(result, -lshift)
	}
	if lshift < 32 {
		return result >> lshift
	}
	return 0
}

func silkCLZ32(x int32) int32 {
	return int32(bits.LeadingZeros32(uint32(x)))
}

func silkCLZ_FRAC(in int32) (lz int32, fracQ7 int32) {
	lz = silkCLZ32(in)
	rot := bits.RotateLeft32(uint32(in), -int(24-lz))
	fracQ7 = int32(rot & 0x7f)
	return lz, fracQ7
}

func silkFixConst(x float64, q int) int {
	if q < 0 {
		return int(x)
	}
	return int(x*float64(int64(1)<<q) + 0.5)
}
