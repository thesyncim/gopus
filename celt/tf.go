package celt

import (
	"math"

	"github.com/thesyncim/gopus/rangecoding"
)

// ComputeImportance computes per-band importance weights for TF analysis.
// Importance weights affect how much each band's TF decision matters in the Viterbi search.
//
// The importance is derived from:
// - Band log energies relative to a spectral follower curve
// - Noise floor based on band width and quantization depth
// - Masking effects from neighboring bands
//
// Higher importance means the band's TF decision has more perceptual impact.
// Default value is 13 (when no analysis is performed).
//
// Parameters:
//   - bandLogE: log-domain band energies (mean-relative, from ComputeBandEnergies)
//   - oldBandE: previous frame band energies (for temporal smoothing)
//   - nbBands: number of bands
//   - channels: number of audio channels
//   - lm: log mode (frame size index)
//   - lsbDepth: bit depth of input signal (typically 16 or 24)
//   - effectiveBytes: available bytes for encoding
//
// Returns: per-band importance weights (13 = neutral, higher = more important)
//
// Reference: libopus celt/celt_encoder.c dynalloc_analysis() importance calculation
func ComputeImportance(bandLogE, oldBandE []float64, nbBands, channels, lm, lsbDepth, effectiveBytes int) []int {
	importance := make([]int, nbBands)

	// Default importance when analysis is disabled (low bitrate or complexity)
	// libopus: if (effectiveBytes < (30 + 5*LM)) importance[i] = 13
	if effectiveBytes < 30+5*lm {
		for i := 0; i < nbBands; i++ {
			importance[i] = 13
		}
		return importance
	}

	// Compute noise floor per band
	// libopus: noise_floor[i] = 0.0625*logN[i] + 0.5 + (9-lsb_depth) - eMeans[i]/16 + 0.0062*(i+5)^2
	noiseFloor := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		logNVal := 0.0
		if i < len(LogN) {
			logNVal = float64(LogN[i]) / 256.0 // LogN is in Q8
		}
		eMean := 0.0
		if i < len(eMeans) {
			eMean = eMeans[i]
		}
		// Noise floor formula from libopus (converted from fixed-point)
		noiseFloor[i] = 0.0625*logNVal + 0.5 + float64(9-lsbDepth) - eMean/16.0 + 0.0062*float64((i+5)*(i+5))
	}

	// Compute max depth across all bands and channels
	maxDepth := -31.9
	end := nbBands
	for c := 0; c < channels; c++ {
		for i := 0; i < end; i++ {
			idx := c*nbBands + i
			if idx < len(bandLogE) {
				depth := bandLogE[idx] - noiseFloor[i]
				if depth > maxDepth {
					maxDepth = depth
				}
			}
		}
	}

	// Compute follower curve (spectral envelope tracker)
	// This implements a simple masking model
	follower := make([]float64, nbBands)

	// For each channel, compute follower and combine
	for c := 0; c < channels; c++ {
		bandLogE3 := make([]float64, nbBands)
		f := make([]float64, nbBands)

		// Get band energies for this channel
		for i := 0; i < nbBands; i++ {
			idx := c*nbBands + i
			if idx < len(bandLogE) {
				bandLogE3[i] = bandLogE[idx]
			}
			// For LM=0, use max of current and previous frame energies
			// (single-bin bands have high variance)
			if lm == 0 && i < 8 {
				oldIdx := c*MaxBands + i
				if oldIdx < len(oldBandE) && oldBandE[oldIdx] > bandLogE3[i] {
					bandLogE3[i] = oldBandE[oldIdx]
				}
			}
		}

		// Forward pass: follower tracks rising edges with limited slope
		f[0] = bandLogE3[0]
		last := 0
		for i := 1; i < nbBands; i++ {
			// Track last band significantly higher than previous
			if bandLogE3[i] > bandLogE3[i-1]+0.5 {
				last = i
			}
			// Follower rises at most 1.5 dB per band
			if f[i-1]+1.5 < bandLogE3[i] {
				f[i] = f[i-1] + 1.5
			} else {
				f[i] = bandLogE3[i]
			}
		}

		// Backward pass: follower tracks falling edges
		for i := last - 1; i >= 0; i-- {
			// Follower falls at most 2 dB per band
			if f[i+1]+2.0 < f[i] {
				f[i] = f[i+1] + 2.0
			}
			if bandLogE3[i] < f[i] {
				f[i] = bandLogE3[i]
			}
		}

		// Clamp follower to noise floor
		for i := 0; i < nbBands; i++ {
			if f[i] < noiseFloor[i] {
				f[i] = noiseFloor[i]
			}
		}

		// Compute importance contribution from this channel
		// follower = max(0, bandLogE - follower)
		if channels == 2 {
			// Stereo: combine with cross-talk consideration (24 dB)
			otherC := 1 - c
			for i := 0; i < nbBands; i++ {
				otherIdx := otherC*nbBands + i
				if otherIdx < len(bandLogE) {
					// Consider 24 dB cross-talk between channels
					otherFollower := follower[i] - 4.0 // 4.0 ~ 24 dB
					if otherFollower > f[i] {
						f[i] = otherFollower
					}
				}
			}
		}

		// Store or combine follower values
		if c == 0 {
			copy(follower, f)
		} else {
			// For stereo, combine the excess energy from both channels
			for i := 0; i < nbBands; i++ {
				idx0 := i
				idx1 := nbBands + i
				excess0 := 0.0
				excess1 := 0.0
				if idx0 < len(bandLogE) {
					excess0 = bandLogE[idx0] - follower[i]
					if excess0 < 0 {
						excess0 = 0
					}
				}
				if idx1 < len(bandLogE) {
					excess1 = bandLogE[idx1] - f[i]
					if excess1 < 0 {
						excess1 = 0
					}
				}
				follower[i] = (excess0 + excess1) / 2.0
			}
		}
	}

	// If mono, compute excess energy
	if channels == 1 {
		for i := 0; i < nbBands; i++ {
			if i < len(bandLogE) {
				excess := bandLogE[i] - follower[i]
				if excess < 0 {
					excess = 0
				}
				follower[i] = excess
			} else {
				follower[i] = 0
			}
		}
	}

	// Convert follower to importance
	// libopus: importance[i] = floor(0.5 + 13 * celt_exp2_db(min(follower[i], 4.0)))
	// celt_exp2_db is just exp2 (2^x) for float builds
	for i := 0; i < nbBands; i++ {
		f := follower[i]
		if f > 4.0 {
			f = 4.0
		}
		// exp2(f) where f is in "dB-like" log2 units
		// When f=0, importance=13. When f=4, importance=13*16=208
		imp := 13.0 * math.Pow(2.0, f)
		importance[i] = int(math.Floor(0.5 + imp))
		// Clamp to reasonable range
		if importance[i] < 1 {
			importance[i] = 1
		}
		if importance[i] > 255 {
			importance[i] = 255
		}
	}

	return importance
}

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

	// Note: Unlike earlier versions, we do NOT skip TF analysis for LM=0.
	// libopus runs tf_analysis for all LM values when enable_tf_analysis is true.
	// For LM=0 (2.5ms frames), the tf_select_table values still allow meaningful
	// TF changes: when per_band_flag=1, the TF value can be -1 instead of 0.

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

// TFAnalysisScratch holds pre-allocated buffers for TF analysis.
type TFAnalysisScratch struct {
	Metric []int     // Per-band metric (size: nbEBands)
	Tmp    []float64 // Band coefficients working buffer
	Tmp1   []float64 // Copy for transient analysis
	Path0  []int     // Viterbi path state 0
	Path1  []int     // Viterbi path state 1
	TfRes  []int     // Output buffer
}

// EnsureTFAnalysisScratch ensures scratch buffers are large enough.
func (s *TFAnalysisScratch) EnsureTFAnalysisScratch(nbEBands, maxBandWidth int) {
	if cap(s.Metric) < nbEBands {
		s.Metric = make([]int, nbEBands)
	} else {
		s.Metric = s.Metric[:nbEBands]
	}
	if cap(s.Tmp) < maxBandWidth {
		s.Tmp = make([]float64, maxBandWidth)
	}
	if cap(s.Tmp1) < maxBandWidth {
		s.Tmp1 = make([]float64, maxBandWidth)
	}
	if cap(s.Path0) < nbEBands {
		s.Path0 = make([]int, nbEBands)
	} else {
		s.Path0 = s.Path0[:nbEBands]
	}
	if cap(s.Path1) < nbEBands {
		s.Path1 = make([]int, nbEBands)
	} else {
		s.Path1 = s.Path1[:nbEBands]
	}
	if cap(s.TfRes) < nbEBands {
		s.TfRes = make([]int, nbEBands)
	} else {
		s.TfRes = s.TfRes[:nbEBands]
	}
}

// TFAnalysisWithScratch is the zero-allocation version of TFAnalysis.
func TFAnalysisWithScratch(X []float64, N0, nbEBands int, isTransient bool, lm int, tfEstimate float64, effectiveBytes int, importance []int, scratch *TFAnalysisScratch) (tfRes []int, tfSelect int) {
	if scratch == nil {
		return TFAnalysis(X, N0, nbEBands, isTransient, lm, tfEstimate, effectiveBytes, importance)
	}

	// Compute max band width for scratch sizing
	maxBandWidth := 0
	for i := 0; i < nbEBands && i+1 < len(EBands); i++ {
		bw := (EBands[i+1] - EBands[i]) << lm
		if bw > maxBandWidth {
			maxBandWidth = bw
		}
	}
	scratch.EnsureTFAnalysisScratch(nbEBands, maxBandWidth)

	tfRes = scratch.TfRes[:nbEBands]
	for i := range tfRes {
		tfRes[i] = 0
	}

	// Compute lambda
	lambda := 80
	if effectiveBytes > 0 {
		lambda = maxInt(80, 20480/effectiveBytes+2)
	}

	bias := 0.04 * math.Max(-0.25, 0.5-tfEstimate)

	metric := scratch.Metric[:nbEBands]
	tmp := scratch.Tmp

	for i := 0; i < nbEBands; i++ {
		bandStart := EBands[i] << lm
		bandEnd := EBands[i+1] << lm
		N := bandEnd - bandStart

		narrow := (EBands[i+1] - EBands[i]) == 1

		// Use scratch buffer
		tmpSlice := tmp[:N]
		for j := 0; j < N && bandStart+j < len(X); j++ {
			tmpSlice[j] = X[bandStart+j]
		}

		var initLM int
		if isTransient {
			initLM = lm
		}
		L1 := l1Metric(tmpSlice, N, initLM, bias)
		bestL1 := L1
		bestLevel := 0

		if isTransient && !narrow {
			// Use scratch tmp1 instead of allocating
			tmp1 := scratch.Tmp1[:N]
			copy(tmp1, tmpSlice)
			haar1(tmp1, N>>lm, 1<<lm)
			L1 = l1Metric(tmp1, N, lm+1, bias)
			if L1 < bestL1 {
				bestL1 = L1
				bestLevel = -1
			}
		}

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

			haar1(tmpSlice, N>>k, 1<<k)
			L1 = l1Metric(tmpSlice, N, B, bias)

			if L1 < bestL1 {
				bestL1 = L1
				bestLevel = k + 1
			}
		}

		if isTransient {
			metric[i] = 2 * bestLevel
		} else {
			metric[i] = -2 * bestLevel
		}

		if narrow && (metric[i] == 0 || metric[i] == -2*lm) {
			metric[i]--
		}
	}

	// Search for optimal tf resolution using Viterbi
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

	if selcost[1] < selcost[0] && isTransient {
		tfSelect = 1
	}

	// Viterbi forward pass
	isTransientInt := boolToInt(isTransient)
	path0 := scratch.Path0[:nbEBands]
	path1 := scratch.Path1[:nbEBands]

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

// TFDecodeForTest exposes tfDecode for cross-package tests (e.g., CGO comparisons).
// It should not be used in production code.
func TFDecodeForTest(start, end int, isTransient bool, tfRes []int, lm int, rd *rangecoding.Decoder) {
	tfDecode(start, end, isTransient, tfRes, lm, rd)
}
