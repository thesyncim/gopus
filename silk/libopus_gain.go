package silk

const (
	qgainRangeQ7   = ((maxQGainDb - minQGainDb) * 128) / 6
	gainOffsetQ7   = (minQGainDb*128)/6 + 16*128
	invScaleQ16Val = (1 << 16) * qgainRangeQ7 / (nLevelsQGain - 1)
)

func silkGainsDequant(gainsQ16 *[maxNbSubfr]int32, indices *[maxNbSubfr]int8, prevIndex *int8, conditional bool, nbSubfr int) {
	prev := int(*prevIndex)
	for k := 0; k < nbSubfr; k++ {
		if k == 0 && !conditional {
			base := prev - 16
			if base < int(indices[k]) {
				base = int(indices[k])
			}
			prev = base
		} else {
			indTmp := int(indices[k]) + minDeltaGainQuant
			doubleStep := 2*maxDeltaGainQuant - nLevelsQGain + prev
			if indTmp > doubleStep {
				prev += (indTmp << 1) - doubleStep
			} else {
				prev += indTmp
			}
		}
		prev = silkLimitInt(prev, 0, nLevelsQGain-1)
		logGainQ7 := silkSMULWB(int32(invScaleQ16Val), int32(prev)) + int32(gainOffsetQ7)
		if logGainQ7 > 3967 {
			logGainQ7 = 3967
		}
		gainsQ16[k] = silkLog2Lin(logGainQ7)
	}
	*prevIndex = int8(prev)
}
