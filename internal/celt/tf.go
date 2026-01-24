package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

func tfDecode(start, end int, isTransient bool, tfRes []int, lm int, rd *rangecoding.Decoder) {
	if rd == nil {
		return
	}
	budget := rd.StorageBits()
	tell := rd.Tell()
	logp := 4
	if isTransient {
		logp = 2
	}
	tfSelectRsv := lm > 0 && tell+logp+1 <= budget
	if tfSelectRsv {
		budget--
	}
	tfChanged := 0
	curr := 0
	for i := start; i < end; i++ {
		if tell+logp <= budget {
			curr ^= rd.DecodeBit(uint(logp))
			tell = rd.Tell()
			if curr != 0 {
				tfChanged = 1
			}
		}
		tfRes[i] = curr
		if isTransient {
			logp = 4
		} else {
			logp = 5
		}
	}
	tfSelect := 0
	if tfSelectRsv {
		idx0 := tfSelectTable[lm][4*boolToInt(isTransient)+0+tfChanged]
		idx1 := tfSelectTable[lm][4*boolToInt(isTransient)+2+tfChanged]
		if idx0 != idx1 {
			tfSelect = rd.DecodeBit(1)
		}
	}
	for i := start; i < end; i++ {
		idx := 4*boolToInt(isTransient) + 2*tfSelect + tfRes[i]
		tfRes[i] = int(tfSelectTable[lm][idx])
	}
}
