package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// l1Metric computes the L1 metric for TF analysis with a bias term.
// The bias favors frequency resolution when in doubt.
//
// Reference: libopus celt/celt_encoder.c l1_metric()
func l1Metric(tmp []float64, N int, LM int, bias float64) float64 {
	var L1 float64
	for i := 0; i < N && i < len(tmp); i++ {
		L1 += math.Abs(tmp[i])
	}
	// When in doubt, prefer good frequency resolution
	L1 = L1 + float64(LM)*bias*L1
	return L1
}

// TFAnalysis performs time-frequency analysis to determine optimal TF resolution per band.
// It uses a Viterbi algorithm to find the best TF resolution settings.
//
// Parameters:
//   - X: normalized MDCT coefficients
//   - N0: total number of coefficients (per channel)
//   - nbEBands: number of bands to analyze
//   - isTransient: whether frame uses short blocks
//   - lm: log mode (frame size index)
//   - tfEstimate: estimate of temporal vs frequency content (0.0 = time, 1.0 = freq)
//   - effectiveBytes: available bytes for encoding (affects lambda)
//   - importance: per-band importance weights (nil for uniform)
//
// Returns:
//   - tfRes: per-band TF resolution flags (0 or 1)
//   - tfSelect: TF select flag for bitstream
//
// Reference: libopus celt/celt_encoder.c tf_analysis()
func TFAnalysis(X []float64, N0, nbEBands int, isTransient bool, lm int, tfEstimate float64, effectiveBytes int, importance []int) (tfRes []int, tfSelect int) {
	tfRes = make([]int, nbEBands)

	// For LM=0 (2.5ms frames), always use default TF settings
	if lm == 0 {
		return tfRes, 0
	}

	// Compute lambda (transition cost) based on available bits
	// Higher lambda = more expensive to change TF resolution between bands
	lambda := 80
	if effectiveBytes > 0 {
		lambda = maxInt(80, 20480/effectiveBytes+2)
	}

	// Compute bias: slightly prefer frequency resolution when uncertain
	// bias = 0.04 * max(-0.25, 0.5 - tfEstimate)
	bias := 0.04 * math.Max(-0.25, 0.5-tfEstimate)

	// Compute per-band metric
	metric := make([]int, nbEBands)
	tmp := make([]float64, 0, (EBands[nbEBands]-EBands[nbEBands-1])<<lm)

	for i := 0; i < nbEBands; i++ {
		bandStart := EBands[i] << lm
		bandEnd := EBands[i+1] << lm
		N := bandEnd - bandStart

		// Check if band is too narrow to be split
		narrow := (EBands[i+1] - EBands[i]) == 1

		// Copy band coefficients to tmp
		if cap(tmp) < N {
			tmp = make([]float64, N)
		} else {
			tmp = tmp[:N]
		}

		for j := 0; j < N && bandStart+j < len(X); j++ {
			tmp[j] = X[bandStart+j]
		}

		// Compute initial L1 metric
		var initLM int
		if isTransient {
			initLM = lm
		}
		L1 := l1Metric(tmp, N, initLM, bias)
		bestL1 := L1
		bestLevel := 0

		// Check the -1 case for transients (more time resolution)
		if isTransient && !narrow {
			tmp1 := make([]float64, N)
			copy(tmp1, tmp)
			haar1(tmp1, N>>lm, 1<<lm)
			L1 = l1Metric(tmp1, N, lm+1, bias)
			if L1 < bestL1 {
				bestL1 = L1
				bestLevel = -1
			}
		}

		// Try different Haar levels
		maxK := lm
		if !isTransient && !narrow {
			maxK = lm + 1
		}
		for k := 0; k < maxK; k++ {
			var B int
			if isTransient {
				B = lm - k - 1
			} else {
				B = k + 1
			}

			haar1(tmp, N>>k, 1<<k)
			L1 = l1Metric(tmp, N, B, bias)

			if L1 < bestL1 {
				bestL1 = L1
				bestLevel = k + 1
			}
		}

		// Convert to metric in Q1 format
		if isTransient {
			metric[i] = 2 * bestLevel
		} else {
			metric[i] = -2 * bestLevel
		}

		// For narrow bands, set metric to half-way point to avoid biasing
		if narrow && (metric[i] == 0 || metric[i] == -2*lm) {
			metric[i]--
		}
	}

	// Search for optimal tf resolution using Viterbi algorithm
	// First, determine tf_select by comparing costs for both options
	tfSelect = 0
	selcost := [2]int{}

	for sel := 0; sel < 2; sel++ {
		imp0 := 13
		if importance != nil && len(importance) > 0 {
			imp0 = importance[0]
		}
		isTransientInt := boolToInt(isTransient)

		cost0 := imp0 * absInt(metric[0]-2*int(tfSelectTable[lm][4*isTransientInt+2*sel+0]))
		lambdaInit := lambda
		if isTransient {
			lambdaInit = 0
		}
		cost1 := imp0*absInt(metric[0]-2*int(tfSelectTable[lm][4*isTransientInt+2*sel+1])) + lambdaInit

		for i := 1; i < nbEBands; i++ {
			imp := 13
			if importance != nil && i < len(importance) {
				imp = importance[i]
			}

			curr0 := minInt(cost0, cost1+lambda)
			curr1 := minInt(cost0+lambda, cost1)
			cost0 = curr0 + imp*absInt(metric[i]-2*int(tfSelectTable[lm][4*isTransientInt+2*sel+0]))
			cost1 = curr1 + imp*absInt(metric[i]-2*int(tfSelectTable[lm][4*isTransientInt+2*sel+1]))
		}

		selcost[sel] = minInt(cost0, cost1)
	}

	// Only allow tf_select=1 for transients (conservative approach per libopus)
	if selcost[1] < selcost[0] && isTransient {
		tfSelect = 1
	}

	// Viterbi forward pass with selected tf_select
	isTransientInt := boolToInt(isTransient)
	path0 := make([]int, nbEBands)
	path1 := make([]int, nbEBands)

	imp0 := 13
	if importance != nil && len(importance) > 0 {
		imp0 = importance[0]
	}

	cost0 := imp0 * absInt(metric[0]-2*int(tfSelectTable[lm][4*isTransientInt+2*tfSelect+0]))
	lambdaInit := lambda
	if isTransient {
		lambdaInit = 0
	}
	cost1 := imp0*absInt(metric[0]-2*int(tfSelectTable[lm][4*isTransientInt+2*tfSelect+1])) + lambdaInit

	for i := 1; i < nbEBands; i++ {
		imp := 13
		if importance != nil && i < len(importance) {
			imp = importance[i]
		}

		// Path for state 0
		from0 := cost0
		from1 := cost1 + lambda
		var curr0 int
		if from0 < from1 {
			curr0 = from0
			path0[i] = 0
		} else {
			curr0 = from1
			path0[i] = 1
		}

		// Path for state 1
		from0 = cost0 + lambda
		from1 = cost1
		var curr1 int
		if from0 < from1 {
			curr1 = from0
			path1[i] = 0
		} else {
			curr1 = from1
			path1[i] = 1
		}

		cost0 = curr0 + imp*absInt(metric[i]-2*int(tfSelectTable[lm][4*isTransientInt+2*tfSelect+0]))
		cost1 = curr1 + imp*absInt(metric[i]-2*int(tfSelectTable[lm][4*isTransientInt+2*tfSelect+1]))
	}

	// Determine final state
	if cost0 < cost1 {
		tfRes[nbEBands-1] = 0
	} else {
		tfRes[nbEBands-1] = 1
	}

	// Viterbi backward pass
	for i := nbEBands - 2; i >= 0; i-- {
		if tfRes[i+1] == 1 {
			tfRes[i] = path1[i+1]
		} else {
			tfRes[i] = path0[i+1]
		}
	}

	return tfRes, tfSelect
}

// TFEncodeWithSelect encodes TF resolution with a specific tf_select value.
// This is called after TFAnalysis to encode the computed TF decisions.
//
// Parameters:
//   - re: range encoder
//   - start, end: band range
//   - isTransient: whether frame uses short blocks
//   - tfRes: per-band TF resolution flags (0 or 1)
//   - lm: log mode (frame size index)
//   - tfSelect: TF select flag from TFAnalysis
//
// Reference: libopus celt/celt_encoder.c tf_encode()
func TFEncodeWithSelect(re *rangecoding.Encoder, start, end int, isTransient bool, tfRes []int, lm int, tfSelect int) {
	if re == nil {
		return
	}

	budget := re.StorageBits()
	tell := re.Tell()
	logp := 4
	if isTransient {
		logp = 2
	}

	// Reserve bit for tf_select if LM > 0 and there's budget
	tfSelectRsv := lm > 0 && tell+logp+1 <= int(budget)
	if tfSelectRsv {
		budget--
	}

	curr := 0
	tfChanged := 0

	for i := start; i < end; i++ {
		if tell+logp <= int(budget) {
			// Encode XOR of current tf_res with previous
			change := tfRes[i] ^ curr
			re.EncodeBit(change, uint(logp))
			tell = re.Tell()
			curr = tfRes[i]
			tfChanged |= curr
		} else {
			// Not enough budget, force to current value
			tfRes[i] = curr
		}

		if isTransient {
			logp = 4
		} else {
			logp = 5
		}
	}

	// Encode tf_select if reserved and it makes a difference
	isTransientInt := boolToInt(isTransient)
	if tfSelectRsv && tfSelectTable[lm][4*isTransientInt+0+tfChanged] != tfSelectTable[lm][4*isTransientInt+2+tfChanged] {
		re.EncodeBit(tfSelect, 1)
	} else {
		tfSelect = 0
	}

	// Convert tfRes to actual TF change values using tfSelectTable
	for i := start; i < end; i++ {
		idx := 4*isTransientInt + 2*tfSelect + tfRes[i]
		tfRes[i] = int(tfSelectTable[lm][idx])
	}
}

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
