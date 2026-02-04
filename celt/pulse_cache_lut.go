package celt

const (
	pulseCacheMinLM = -1
	pulseCacheMaxLM = 3
	pulseCacheLMs   = pulseCacheMaxLM - pulseCacheMinLM + 1
)

type pulseCacheLUT struct {
	cache        []uint8
	bitsToPulses []uint8
	maxBits      int
	maxPseudo    int
}

var pulseCacheLUTs [pulseCacheLMs][MaxBands]pulseCacheLUT

func init() {
	initPulseCacheLUTs()
}

func initPulseCacheLUTs() {
	for lm := pulseCacheMinLM; lm <= pulseCacheMaxLM; lm++ {
		lmIndex := lm - pulseCacheMinLM
		for band := 0; band < MaxBands; band++ {
			cache, ok := pulseCacheForBand(band, lm)
			if !ok || len(cache) == 0 {
				continue
			}
			maxPseudo := int(cache[0])
			if maxPseudo <= 0 || maxPseudo >= len(cache) {
				continue
			}
			maxBits := 0
			for i := 1; i <= maxPseudo; i++ {
				if v := int(cache[i]); v > maxBits {
					maxBits = v
				}
			}
			maxBitsInput := maxBits + 1
			lut := make([]uint8, maxBitsInput+1)
			for bitsQ3 := 1; bitsQ3 <= maxBitsInput; bitsQ3++ {
				lut[bitsQ3] = uint8(bitsToPulsesCachedFast(cache, bitsQ3))
			}
			pulseCacheLUTs[lmIndex][band] = pulseCacheLUT{
				cache:        cache,
				bitsToPulses: lut,
				maxBits:      maxBits,
				maxPseudo:    maxPseudo,
			}
		}
	}
}

func pulseCacheLUTForBand(band, lm int) *pulseCacheLUT {
	if band < 0 || band >= MaxBands {
		return nil
	}
	if lm < pulseCacheMinLM || lm > pulseCacheMaxLM {
		return nil
	}
	lut := &pulseCacheLUTs[lm-pulseCacheMinLM][band]
	if lut.cache == nil {
		return nil
	}
	return lut
}
