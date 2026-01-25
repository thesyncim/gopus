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
	return (x + (1 << (shift - 1))) >> shift
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

func silkDiv32VarQ(a, b int32, q int) int32 {
	if b == 0 {
		return 0
	}
	return int32((float64(a) / float64(b)) * float64(int64(1)<<q))
}

func silkInverse32VarQ(b int32, q int) int32 {
	if b == 0 {
		return 0
	}
	return int32((1.0 / float64(b)) * float64(int64(1)<<q))
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
