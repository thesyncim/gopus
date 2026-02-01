package celt

const (
	maxPseudo    = 40
	logMaxPseudo = 6
)

func getPulses(i int) int {
	if i < 8 {
		return i
	}
	return (8 + (i & 7)) << ((i >> 3) - 1)
}

func pulseCacheForBand(band, lm int) ([]uint8, bool) {
	if band < 0 || band >= MaxBands {
		return nil, false
	}
	if lm < -1 {
		return nil, false
	}
	idx := (lm + 1) * MaxBands
	if idx < 0 || idx >= len(cacheIndex50) {
		return nil, false
	}
	start := int(cacheIndex50[idx+band])
	if start < 0 || start >= len(cacheBits50) {
		return nil, false
	}
	cache := cacheBits50[start:]
	if len(cache) == 0 {
		return nil, false
	}
	maxPseudo := int(cache[0])
	if maxPseudo <= 0 || maxPseudo >= len(cache) {
		return nil, false
	}
	return cache, true
}

func bitsToPulses(band, lm, bitsQ3 int) int {
	if bitsQ3 <= 0 {
		return 0
	}
	cache, ok := pulseCacheForBand(band, lm)
	if !ok {
		return 0
	}

	bitsQ3--
	lo := 0
	hi := int(cache[0])
	for i := 0; i < logMaxPseudo; i++ {
		mid := (lo + hi + 1) >> 1
		if int(cache[mid]) >= bitsQ3 {
			hi = mid
		} else {
			lo = mid
		}
	}

	loBits := -1
	if lo > 0 {
		loBits = int(cache[lo])
	}
	if bitsQ3-loBits <= int(cache[hi])-bitsQ3 {
		return lo
	}
	return hi
}

func pulsesToBits(band, lm, pulses int) int {
	if pulses <= 0 {
		return 0
	}
	cache, ok := pulseCacheForBand(band, lm)
	if !ok {
		return 0
	}

	maxPseudo := int(cache[0])
	if pulses > maxPseudo {
		pulses = maxPseudo
	}
	return int(cache[pulses]) + 1
}
