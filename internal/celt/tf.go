package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// tfEncode encodes time-frequency resolution flags for each band.
// This is the inverse of tfDecode.
//
// For simplicity (default case), we encode all zeros meaning no TF changes.
// The tfRes parameter can specify per-band TF values if needed.
//
// Parameters:
//   - re: range encoder
//   - start, end: band range
//   - isTransient: whether frame uses short blocks
//   - tfRes: per-band TF resolution values (optional, nil = all zeros)
//   - lm: log mode (frame size index)
func tfEncode(re *rangecoding.Encoder, start, end int, isTransient bool, tfRes []int, lm int) {
	if re == nil {
		return
	}

	// Initial logp depends on transient mode
	logp := 4
	if isTransient {
		logp = 2
	}

	// Reserve bit for tfSelect if lm > 0
	tfSelectRsv := lm > 0
	tfChanged := 0
	curr := 0

	for i := start; i < end; i++ {
		// Target value for this band (0 if no tfRes provided)
		target := 0
		if tfRes != nil && i < len(tfRes) {
			target = tfRes[i]
		}

		// Encode whether current differs from previous
		change := 0
		if target != curr {
			change = 1
			curr = target
			tfChanged = 1
		}

		re.EncodeBit(change, uint(logp))

		// Update logp for next band
		if isTransient {
			logp = 4
		} else {
			logp = 5
		}
	}

	// Encode tfSelect if reserved and there's a meaningful choice
	if tfSelectRsv {
		idx0 := tfSelectTable[lm][4*boolToInt(isTransient)+0+tfChanged]
		idx1 := tfSelectTable[lm][4*boolToInt(isTransient)+2+tfChanged]
		if idx0 != idx1 {
			// Encode tfSelect = 0 (default)
			re.EncodeBit(0, 1)
		}
	}
}

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
		if t, ok := DefaultTracer.(interface{ TraceTF(band int, val int) }); ok {
			t.TraceTF(i, tfRes[i])
		}
	}
}
