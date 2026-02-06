package silk

import "math"

// Constants matching libopus
const (
	// Conditioning factor for Burg's algorithm (regularization)
	// Per libopus silk/tuning_parameters.h: FIND_LPC_COND_FAC
	findLPCCondFac = 1e-5

	// Maximum frame size for Burg analysis
	maxBurgFrameSize = 384 // subfr_length * nb_subfr = (0.005 * 16000 + 16) * 4

	// Maximum LPC order
	silkMaxOrderLPC = 16

	// Minimum inverse prediction gain (max prediction gain = 1e4)
	// Per libopus: prevents filter instability
	minInvGain = 1e-4
)

// burgModifiedFLP computes LPC coefficients using libopus-matching Burg's method.
// This is a Go implementation of silk_burg_modified_FLP from libopus.
//
// Parameters:
//   - x: Input signal (nb_subfr * subfr_length samples)
//   - minInvGainVal: Minimum inverse prediction gain (typically 1e-4 for 40dB max gain)
//   - subfrLength: Subframe length including D preceding samples
//   - nbSubfr: Number of subframes stacked in x
//   - order: LPC order (D)
//
// Returns: LPC coefficients as float64 slice and residual energy
func burgModifiedFLP(x []float64, minInvGainVal float64, subfrLength, nbSubfr, order int) ([]float64, float64) {
	totalLen := nbSubfr * subfrLength
	if totalLen > maxBurgFrameSize || totalLen > len(x) {
		// Safety check - can't process
		return make([]float64, order), 0
	}

	// Output LPC coefficients (Af in libopus)
	Af := make([]float64, order)

	// Compute total energy (C0)
	var C0 float64
	for i := 0; i < totalLen; i++ {
		C0 += x[i] * x[i]
	}

	// Initialize correlation rows
	CFirstRow := make([]float64, silkMaxOrderLPC)
	CLastRow := make([]float64, silkMaxOrderLPC)

	// Compute initial autocorrelations, added over subframes
	for s := 0; s < nbSubfr; s++ {
		xPtr := s * subfrLength
		for n := 1; n <= order; n++ {
			var sum float64
			for k := 0; k < subfrLength-n; k++ {
				sum += x[xPtr+k] * x[xPtr+k+n]
			}
			CFirstRow[n-1] += sum
		}
	}
	copy(CLastRow, CFirstRow)

	// Initialize CAf and CAb (correlation * filter)
	CAf := make([]float64, silkMaxOrderLPC+1)
	CAb := make([]float64, silkMaxOrderLPC+1)
	condFac := float64(float32(findLPCCondFac))
	eps := float64(float32(1e-9))
	CAf[0] = C0 + condFac*C0 + eps
	CAb[0] = CAf[0]

	invGain := 1.0
	reachedMaxGain := false

	// Main Burg iteration
	for n := 0; n < order; n++ {
		// Update correlation rows and C*Af, C*flipud(Af)
		for s := 0; s < nbSubfr; s++ {
			xPtr := s * subfrLength
			tmp1 := x[xPtr+n]
			tmp2 := x[xPtr+subfrLength-n-1]

			for k := 0; k < n; k++ {
				CFirstRow[k] -= x[xPtr+n] * x[xPtr+n-k-1]
				CLastRow[k] -= x[xPtr+subfrLength-n-1] * x[xPtr+subfrLength-n+k]
				Atmp := Af[k]
				tmp1 += x[xPtr+n-k-1] * Atmp
				tmp2 += x[xPtr+subfrLength-n+k] * Atmp
			}

			for k := 0; k <= n; k++ {
				CAf[k] -= tmp1 * x[xPtr+n-k]
				CAb[k] -= tmp2 * x[xPtr+subfrLength-n+k-1]
			}
		}

		// Update CAf[n+1] and CAb[n+1]
		tmp1 := CFirstRow[n]
		tmp2 := CLastRow[n]
		for k := 0; k < n; k++ {
			Atmp := Af[k]
			tmp1 += CLastRow[n-k-1] * Atmp
			tmp2 += CFirstRow[n-k-1] * Atmp
		}
		CAf[n+1] = tmp1
		CAb[n+1] = tmp2

		// Calculate numerator and denominator for reflection coefficient
		num := CAb[n+1]
		nrgB := CAb[0]
		nrgF := CAf[0]
		for k := 0; k < n; k++ {
			Atmp := Af[k]
			num += CAb[n-k] * Atmp
			nrgB += CAb[k+1] * Atmp
			nrgF += CAf[k+1] * Atmp
		}

		if nrgF <= 0 || nrgB <= 0 {
			break
		}

		// Calculate reflection coefficient
		rc := -2.0 * num / (nrgF + nrgB)

		// Update inverse prediction gain
		tmp1 = invGain * (1.0 - rc*rc)
		if tmp1 <= minInvGainVal {
			// Max prediction gain exceeded; set rc such that max gain is exactly hit
			rc = math.Sqrt(1.0 - minInvGainVal/invGain)
			if num > 0 {
				rc = -rc
			}
			invGain = minInvGainVal
			reachedMaxGain = true
		} else {
			invGain = tmp1
		}

		// Update AR coefficients using Levinson-Durbin recursion
		for k := 0; k < (n+1)>>1; k++ {
			tmp1 = Af[k]
			tmp2 = Af[n-k-1]
			Af[k] = tmp1 + rc*tmp2
			Af[n-k-1] = tmp2 + rc*tmp1
		}
		Af[n] = rc

		if reachedMaxGain {
			// Set remaining coefficients to zero
			for k := n + 1; k < order; k++ {
				Af[k] = 0
			}
			break
		}

		// Update C*Af and C*Ab
		for k := 0; k <= n+1; k++ {
			tmp1 = CAf[k]
			CAf[k] += rc * CAb[n-k+1]
			CAb[n-k+1] += rc * tmp1
		}
	}

	// Compute residual energy
	var nrgF float64
	if reachedMaxGain {
		// Subtract energy of preceding samples from C0
		for s := 0; s < nbSubfr; s++ {
			xPtr := s * subfrLength
			for k := 0; k < order; k++ {
				C0 -= x[xPtr+k] * x[xPtr+k]
			}
		}
		nrgF = C0 * invGain
	} else {
		// Compute residual energy from CAf and Af
		nrgF = CAf[0]
		var tmp1 float64 = 1.0
		for k := 0; k < order; k++ {
			Atmp := Af[k]
			nrgF += CAf[k+1] * Atmp
			tmp1 += Atmp * Atmp
		}
		nrgF -= condFac * C0 * tmp1
	}

	// Negate coefficients for LPC convention (libopus stores negative)
	// Match libopus: A[k] = (silk_float)(-Af[k]) and return (silk_float)nrg_f
	A := make([]float64, order)
	for k := 0; k < order; k++ {
		A[k] = float64(float32(-Af[k]))
	}

	return A, float64(float32(nrgF))
}

// burgLPC computes LPC coefficients using Burg's method.
// Burg's method minimizes both forward and backward prediction error,
// providing better numerical stability than autocorrelation method.
//
// Per draft-vos-silk-01 Section 2.1.2.1.
//
// signal: Input PCM samples (assumed normalized or in [-1,1] for best results)
// order: LPC order (10 for NB/MB, 16 for WB)
// Returns: LPC coefficients in Q12 format
func burgLPC(signal []float32, order int) []int16 {
	n := len(signal)
	if n < order+1 {
		// Not enough samples, return zeros
		return make([]int16, order)
	}

	// Convert to float64 for precision
	x := make([]float64, n)
	for i := 0; i < n; i++ {
		x[i] = float64(signal[i])
	}

	// Use subframe-based Burg method matching libopus
	// For a single analysis window, treat as 1 subframe
	subfrLength := n
	nbSubfr := 1

	// If signal is long enough, use 4 subframes like libopus
	if n >= order*4 {
		nbSubfr = 4
		subfrLength = n / nbSubfr
	}

	a, _ := burgModifiedFLP(x, minInvGain, subfrLength, nbSubfr, order)

	// Convert to Q12 fixed-point
	lpcQ12 := make([]int16, order)
	for i := 0; i < order; i++ {
		val := float64(float32(a[i]) * 4096.0) // Q12 scaling
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		lpcQ12[i] = int16(val)
	}

	return lpcQ12
}

// applySineWindowFLP applies asymmetric sine window to signal.
// This matches libopus silk_apply_sine_window_FLP.
//
// winType: 1 -> sine window from 0 to pi/2 (ramp up)
//
//	2 -> sine window from pi/2 to pi (ramp down)
func applySineWindowFLP(pxWin, px []float64, winType, length int) {
	if length == 0 || length&3 != 0 {
		return
	}

	freq := math.Pi / float64(length+1)

	// Approximation of 2 * cos(f)
	c := 2.0 - freq*freq

	var S0, S1 float64
	if winType < 2 {
		// Start from 0
		S0 = 0.0
		S1 = freq // Approximation of sin(f)
	} else {
		// Start from 1
		S0 = 1.0
		S1 = 0.5 * c // Approximation of cos(f)
	}

	// Recursive sine computation: sin(n*f) = 2*cos(f)*sin((n-1)*f) - sin((n-2)*f)
	for k := 0; k < length; k += 4 {
		pxWin[k+0] = px[k+0] * 0.5 * (S0 + S1)
		pxWin[k+1] = px[k+1] * S1
		S0 = c*S1 - S0
		pxWin[k+2] = px[k+2] * 0.5 * (S1 + S0)
		pxWin[k+3] = px[k+3] * S0
		S1 = c*S0 - S1
	}
}

// lpcAnalysisFilterFLP applies LPC analysis filter to compute residual.
// This matches libopus silk_LPC_analysis_filter_FLP.
// First Order samples of output are set to zero.
func lpcAnalysisFilterFLP(rLPC, predCoef, s []float64, length, order int) {
	if order > length {
		return
	}

	// Set first Order output samples to zero
	for i := 0; i < order; i++ {
		rLPC[i] = 0
	}

	// Apply analysis filter
	for ix := order; ix < length; ix++ {
		var lpcPred float32
		for k := 0; k < order; k++ {
			lpcPred += float32(s[ix-k-1]) * float32(predCoef[k])
		}
		rLPC[ix] = float64(float32(s[ix]) - lpcPred)
	}
}

// energyF64 computes energy of a float64 signal.
func energyF64(x []float64, length int) float64 {
	var energy float64
	for i := 0; i < length; i++ {
		energy += x[i] * x[i]
	}
	return energy
}

// energyF32 computes energy of a float32 signal using libopus-style unrolling.
func energyF32(x []float32, length int) float64 {
	var energy float64
	i := 0
	for i < length-3 {
		d0 := float64(x[i+0])
		d1 := float64(x[i+1])
		d2 := float64(x[i+2])
		d3 := float64(x[i+3])
		energy += d0*d0 + d1*d1 + d2*d2 + d3*d3
		i += 4
	}
	for ; i < length; i++ {
		d := float64(x[i])
		energy += d * d
	}
	return energy
}

// innerProductF32 computes the inner product of float32 signals using libopus ordering.
func innerProductF32(a, b []float32, length int) float64 {
	var result float64
	i := 0
	for i < length-3 {
		result += float64(a[i+0])*float64(b[i+0]) +
			float64(a[i+1])*float64(b[i+1]) +
			float64(a[i+2])*float64(b[i+2]) +
			float64(a[i+3])*float64(b[i+3])
		i += 4
	}
	for ; i < length; i++ {
		result += float64(a[i]) * float64(b[i])
	}
	return result
}

// a2nlsfFLP converts LPC coefficients to NLSF using floating point.
// This matches libopus silk_A2NLSF_FLP / silk_A2NLSF.
func a2nlsfFLP(a []float64, order int) []int16 {
	// Convert float64 LPC to Q16 for the fixed-point A2NLSF
	aQ16 := make([]int32, order)
	for k := 0; k < order; k++ {
		a32 := float32(a[k])
		aQ16[k] = float64ToInt32Round(float64(a32 * 65536.0))
	}

	nlsfQ15 := make([]int16, order)
	silkA2NLSF(nlsfQ15, aQ16, order)
	return nlsfQ15
}

// silkA2NLSF converts LPC coefficients to NLSF.
// This is a Go implementation matching libopus silk/A2NLSF.c
func silkA2NLSF(NLSF []int16, aQ16 []int32, d int) {
	const (
		binDivSteps   = 3
		maxIterations = 16
	)

	dd := d >> 1

	// Initialize P and Q polynomials
	P := make([]int32, dd+1)
	Q := make([]int32, dd+1)

	a2nlsfInit(aQ16, P, Q, dd)

	// Find roots alternating between P and Q
	p := P
	PQ := [2][]int32{P, Q}

	xlo := int32(silk_LSFCosTab_FIX_Q12[0])
	ylo := a2nlsfEvalPoly(p, xlo, dd)

	var rootIx int
	if ylo < 0 {
		NLSF[0] = 0
		p = Q
		ylo = a2nlsfEvalPoly(p, xlo, dd)
		rootIx = 1
	}

	k := 1
	i := 0
	thr := int32(0)

	for {
		// Evaluate polynomial at next table position
		xhi := int32(silk_LSFCosTab_FIX_Q12[k])
		yhi := a2nlsfEvalPoly(p, xhi, dd)

		// Detect zero crossing
		if (ylo <= 0 && yhi >= thr) || (ylo >= 0 && yhi <= -thr) {
			if yhi == 0 {
				thr = 1
			} else {
				thr = 0
			}

			// Binary division to refine root location
			ffrac := int32(-256)
			for m := 0; m < binDivSteps; m++ {
				xmid := silkRSHIFT_ROUND(xlo+xhi, 1)
				ymid := a2nlsfEvalPoly(p, xmid, dd)

				if (ylo <= 0 && ymid >= 0) || (ylo >= 0 && ymid <= 0) {
					xhi = xmid
					yhi = ymid
				} else {
					xlo = xmid
					ylo = ymid
					ffrac += int32(128) >> m
				}
			}

			// Interpolate
			if silkAbs32(ylo) < 65536 {
				den := ylo - yhi
				nom := (ylo << (8 - binDivSteps)) + (den >> 1)
				if den != 0 {
					ffrac += silkDiv32(nom, den)
				}
			} else {
				den := (ylo - yhi) >> (8 - binDivSteps)
				if den != 0 {
					ffrac += silkDiv32(ylo, den)
				}
			}

			val := (int32(k) << 8) + ffrac
			if val > 32767 {
				val = 32767
			}
			NLSF[rootIx] = int16(val)

			rootIx++
			if rootIx >= d {
				// Found all roots
				return
			}

			// Alternate polynomial
			p = PQ[rootIx&1]

			// Restart search from previous position
			xlo = int32(silk_LSFCosTab_FIX_Q12[k-1])
			if rootIx&2 == 0 {
				ylo = 1 << 12
			} else {
				ylo = -1 << 12
			}
		} else {
			k++
			xlo = xhi
			ylo = yhi
			thr = 0

			if k > lsfCosTabSizeFix {
				i++
				if i > maxIterations {
					// Set NLSFs to white spectrum
					spacing := int16((1 << 15) / int32(d+1))
					NLSF[0] = spacing
					for n := 1; n < d; n++ {
						NLSF[n] = NLSF[n-1] + spacing
					}
					return
				}

				// Apply bandwidth expansion and retry
				silkBwExpander32AQ16(aQ16, d, 65536-int32(1<<i))
				a2nlsfInit(aQ16, P, Q, dd)

				p = P
				xlo = int32(silk_LSFCosTab_FIX_Q12[0])
				ylo = a2nlsfEvalPoly(p, xlo, dd)
				if ylo < 0 {
					NLSF[0] = 0
					p = Q
					ylo = a2nlsfEvalPoly(p, xlo, dd)
					rootIx = 1
				} else {
					rootIx = 0
				}
				k = 1
			}
		}
	}
}

// a2nlsfInit initializes P and Q polynomials for A2NLSF.
func a2nlsfInit(aQ16 []int32, P, Q []int32, dd int) {
	// Convert filter coefs to even and odd polynomials
	P[dd] = 1 << 16
	Q[dd] = 1 << 16

	for k := 0; k < dd; k++ {
		P[k] = -aQ16[dd-k-1] - aQ16[dd+k]
		Q[k] = -aQ16[dd-k-1] + aQ16[dd+k]
	}

	// Divide out zeros
	for k := dd; k > 0; k-- {
		P[k-1] -= P[k]
		Q[k-1] += Q[k]
	}

	// Transform polynomials from cos(n*f) to cos(f)^n
	a2nlsfTransPoly(P, dd)
	a2nlsfTransPoly(Q, dd)
}

// a2nlsfTransPoly transforms polynomial from cos(n*f) to cos(f)^n.
func a2nlsfTransPoly(p []int32, dd int) {
	for k := 2; k <= dd; k++ {
		for n := dd; n > k; n-- {
			p[n-2] -= p[n]
		}
		p[k-2] -= p[k] << 1
	}
}

// a2nlsfEvalPoly evaluates polynomial at point x.
func a2nlsfEvalPoly(p []int32, x int32, dd int) int32 {
	y32 := p[dd]
	xQ16 := x << 4

	for n := dd - 1; n >= 0; n-- {
		// y32 = p[n] + (y32 * x_Q16) >> 16
		y32 = int32(int64(p[n]) + ((int64(y32) * int64(xQ16)) >> 16))
	}

	return y32
}

// silkBwExpander32AQ16 applies bandwidth expansion to Q16 LPC coefficients.
func silkBwExpander32AQ16(ar []int32, order int, chirpQ16 int32) {
	if order <= 0 {
		return
	}
	chirpMinusOneQ16 := chirpQ16 - 65536
	for i := 0; i < order-1; i++ {
		ar[i] = silkSMULWW(chirpQ16, ar[i])
		chirpQ16 += silkRSHIFT_ROUND(silkMUL(chirpQ16, chirpMinusOneQ16), 16)
	}
	ar[order-1] = silkSMULWW(chirpQ16, ar[order-1])
}

// applyBandwidthExpansionFloat applies chirp factor to LPC coefficients.
// This prevents filter instability by pulling poles toward origin.
// Per decision D02-03-01: chirp factor 0.96.
//
// lpcQ12: LPC coefficients in Q12 format (modified in place)
// chirp: Expansion factor (0.96 recommended per Phase 2)
func applyBandwidthExpansionFloat(lpcQ12 []int16, chirp float64) {
	factor := chirp
	for i := 0; i < len(lpcQ12); i++ {
		lpcQ12[i] = int16(float64(lpcQ12[i]) * factor)
		factor *= chirp
	}
}

// computeLPCFromFrame computes LPC coefficients for a frame.
// Applies windowing, Burg analysis, and bandwidth expansion.
// Uses scratch buffers for zero-allocation operation.
func (e *Encoder) computeLPCFromFrame(pcm []float32) []int16 {
	// Apply window using scratch buffer
	windowed := ensureFloat32Slice(&e.scratchWindowed, len(pcm))
	n := float64(len(pcm))
	for i := range pcm {
		// Hamming window
		w := 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/(n-1))
		windowed[i] = pcm[i] * float32(w)
	}

	// Compute LPC via Burg's method using scratch buffers
	lpcQ12 := e.burgLPCZeroAlloc(windowed, e.lpcOrder)

	// Apply bandwidth expansion for stability (chirp = 0.96)
	applyBandwidthExpansionFloat(lpcQ12, 0.96)

	return lpcQ12
}

// burgLPCZeroAlloc computes LPC coefficients using scratch buffers.
func (e *Encoder) burgLPCZeroAlloc(signal []float32, order int) []int16 {
	n := len(signal)
	if n < order+1 {
		// Not enough samples, return zeros using scratch
		lpcQ12 := ensureInt16Slice(&e.scratchLpcQ12, order)
		for i := range lpcQ12 {
			lpcQ12[i] = 0
		}
		return lpcQ12
	}

	// Convert to float64 using scratch buffer
	x := ensureFloat64Slice(&e.scratchLpcBurg, n)
	for i := 0; i < n; i++ {
		x[i] = float64(signal[i])
	}

	// Use subframe-based Burg method matching libopus
	subfrLength := n
	nbSubfr := 1

	if n >= order*4 {
		nbSubfr = 4
		subfrLength = n / nbSubfr
	}

	a, _ := e.burgModifiedFLPZeroAlloc(x, minInvGain, subfrLength, nbSubfr, order)

	// Convert to Q12 fixed-point using scratch
	lpcQ12 := ensureInt16Slice(&e.scratchLpcQ12, order)
	for i := 0; i < order; i++ {
		val := float64(float32(a[i]) * 4096.0) // Q12 scaling
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		lpcQ12[i] = int16(val)
	}

	return lpcQ12
}

// burgModifiedFLPZeroAlloc computes LPC using scratch buffers.
func (e *Encoder) burgModifiedFLPZeroAlloc(x []float64, minInvGainVal float64, subfrLength, nbSubfr, order int) ([]float64, float64) {
	totalLen := nbSubfr * subfrLength
	if totalLen > maxBurgFrameSize || totalLen > len(x) {
		// Safety check - return zeros
		result := ensureFloat64Slice(&e.scratchBurgResult, order)
		for i := range result {
			result[i] = 0
		}
		return result, 0
	}

	// Use scratch buffers for Burg algorithm working arrays
	Af := ensureFloat64Slice(&e.scratchBurgAf, order)
	CFirstRow := ensureFloat64Slice(&e.scratchBurgCFirstRow, silkMaxOrderLPC)
	CLastRow := ensureFloat64Slice(&e.scratchBurgCLastRow, silkMaxOrderLPC)
	CAf := ensureFloat64Slice(&e.scratchBurgCAf, silkMaxOrderLPC+1)
	CAb := ensureFloat64Slice(&e.scratchBurgCAb, silkMaxOrderLPC+1)

	// Clear all scratch buffers
	for i := range Af {
		Af[i] = 0
	}
	for i := range CFirstRow {
		CFirstRow[i] = 0
	}
	for i := range CLastRow {
		CLastRow[i] = 0
	}
	for i := range CAf {
		CAf[i] = 0
	}
	for i := range CAb {
		CAb[i] = 0
	}

	// Compute total energy (C0)
	var C0 float64
	for i := 0; i < totalLen; i++ {
		C0 += x[i] * x[i]
	}

	// Compute initial autocorrelations
	for s := 0; s < nbSubfr; s++ {
		xPtr := s * subfrLength
		for n := 1; n <= order; n++ {
			var sum float64
			for k := 0; k < subfrLength-n; k++ {
				sum += x[xPtr+k] * x[xPtr+k+n]
			}
			CFirstRow[n-1] += sum
		}
	}
	copy(CLastRow[:silkMaxOrderLPC], CFirstRow[:silkMaxOrderLPC])

	condFac := float64(float32(findLPCCondFac))
	eps := float64(float32(1e-9))
	CAf[0] = C0 + condFac*C0 + eps
	CAb[0] = CAf[0]

	invGain := 1.0
	reachedMaxGain := false

	// Main Burg iteration
	for n := 0; n < order; n++ {
		for s := 0; s < nbSubfr; s++ {
			xPtr := s * subfrLength
			tmp1 := x[xPtr+n]
			tmp2 := x[xPtr+subfrLength-n-1]

			for k := 0; k < n; k++ {
				CFirstRow[k] -= x[xPtr+n] * x[xPtr+n-k-1]
				CLastRow[k] -= x[xPtr+subfrLength-n-1] * x[xPtr+subfrLength-n+k]
				Atmp := Af[k]
				tmp1 += x[xPtr+n-k-1] * Atmp
				tmp2 += x[xPtr+subfrLength-n+k] * Atmp
			}

			for k := 0; k <= n; k++ {
				CAf[k] -= tmp1 * x[xPtr+n-k]
				CAb[k] -= tmp2 * x[xPtr+subfrLength-n+k-1]
			}
		}

		tmp1 := CFirstRow[n]
		tmp2 := CLastRow[n]
		for k := 0; k < n; k++ {
			Atmp := Af[k]
			tmp1 += CLastRow[n-k-1] * Atmp
			tmp2 += CFirstRow[n-k-1] * Atmp
		}
		CAf[n+1] = tmp1
		CAb[n+1] = tmp2

		num := CAb[n+1]
		nrgB := CAb[0]
		nrgF := CAf[0]
		for k := 0; k < n; k++ {
			Atmp := Af[k]
			num += CAb[n-k] * Atmp
			nrgB += CAb[k+1] * Atmp
			nrgF += CAf[k+1] * Atmp
		}

		if nrgF <= 0 || nrgB <= 0 {
			break
		}

		rc := -2.0 * num / (nrgF + nrgB)

		tmp1 = invGain * (1.0 - rc*rc)
		if tmp1 <= minInvGainVal {
			rc = math.Sqrt(1.0 - minInvGainVal/invGain)
			if num > 0 {
				rc = -rc
			}
			invGain = minInvGainVal
			reachedMaxGain = true
		} else {
			invGain = tmp1
		}

		for k := 0; k < (n+1)>>1; k++ {
			tmp1 = Af[k]
			tmp2 = Af[n-k-1]
			Af[k] = tmp1 + rc*tmp2
			Af[n-k-1] = tmp2 + rc*tmp1
		}
		Af[n] = rc

		if reachedMaxGain {
			for k := n + 1; k < order; k++ {
				Af[k] = 0
			}
			break
		}

		for k := 0; k <= n+1; k++ {
			tmp1 = CAf[k]
			CAf[k] += rc * CAb[n-k+1]
			CAb[n-k+1] += rc * tmp1
		}
	}

	// Store energy and inverse gain for gain computation from prediction residual
	// C0 is the total energy, invGain is the inverse prediction gain
	// Residual energy = C0 * invGain
	// IMPORTANT: C0 is computed from normalized PCM [-1, 1], but gain quantization
	// expects int16-scale energy. Scale by 32768^2 to convert to int16 scale.
	const pcmScaleSq = 32768.0 * 32768.0
	e.lastTotalEnergy = C0 * pcmScaleSq
	e.lastInvGain = invGain
	e.lastNumSamples = totalLen

	var nrgF float64
	if reachedMaxGain {
		// Approximate residual energy (match libopus: subtract energy of preceding samples).
		adjustedC0 := C0
		for s := 0; s < nbSubfr; s++ {
			start := s * subfrLength
			if start+order > totalLen {
				break
			}
			adjustedC0 -= energyF64(x[start:start+order], order)
		}
		nrgF = adjustedC0 * invGain
	} else {
		// Compute residual energy using final correlation state
		nrgF = CAf[0]
		tmp1 := 1.0
		for k := 0; k < order; k++ {
			Atmp := Af[k]
			nrgF += CAf[k+1] * Atmp
			tmp1 += Atmp * Atmp
		}
		nrgF -= condFac * C0 * tmp1
	}

	// Negate coefficients for LPC convention
	A := ensureFloat64Slice(&e.scratchBurgResult, order)
	for k := 0; k < order; k++ {
		A[k] = float64(float32(-Af[k]))
	}

	return A, nrgF
}

// burgModifiedFLPZeroAllocF32 computes LPC using float32 input to match libopus float path.
func (e *Encoder) burgModifiedFLPZeroAllocF32(x []float32, minInvGainVal float32, subfrLength, nbSubfr, order int) ([]float64, float64) {
	totalLen := nbSubfr * subfrLength
	if totalLen > maxBurgFrameSize || totalLen > len(x) {
		result := ensureFloat64Slice(&e.scratchBurgResult, order)
		for i := range result {
			result[i] = 0
		}
		return result, 0
	}

	Af := ensureFloat64Slice(&e.scratchBurgAf, order)
	CFirstRow := ensureFloat64Slice(&e.scratchBurgCFirstRow, silkMaxOrderLPC)
	CLastRow := ensureFloat64Slice(&e.scratchBurgCLastRow, silkMaxOrderLPC)
	CAf := ensureFloat64Slice(&e.scratchBurgCAf, silkMaxOrderLPC+1)
	CAb := ensureFloat64Slice(&e.scratchBurgCAb, silkMaxOrderLPC+1)

	for i := range Af {
		Af[i] = 0
	}
	for i := range CFirstRow {
		CFirstRow[i] = 0
	}
	for i := range CLastRow {
		CLastRow[i] = 0
	}
	for i := range CAf {
		CAf[i] = 0
	}
	for i := range CAb {
		CAb[i] = 0
	}

	C0 := energyF32(x, totalLen)
	for s := 0; s < nbSubfr; s++ {
		xPtr := s * subfrLength
		for n := 1; n <= order; n++ {
			CFirstRow[n-1] += innerProductF32(x[xPtr:], x[xPtr+n:], subfrLength-n)
		}
	}
	copy(CLastRow[:silkMaxOrderLPC], CFirstRow[:silkMaxOrderLPC])

	condFac := float64(float32(findLPCCondFac))
	eps := float64(float32(1e-9))
	CAf[0] = C0 + condFac*C0 + eps
	CAb[0] = CAf[0]

	invGain := 1.0
	reachedMaxGain := false
	minInvGain := float64(minInvGainVal)

	for n := 0; n < order; n++ {
		for s := 0; s < nbSubfr; s++ {
			xPtr := s * subfrLength
			xn := x[xPtr+n]
			xend := x[xPtr+subfrLength-n-1]
			tmp1 := float64(xn)
			tmp2 := float64(xend)

			for k := 0; k < n; k++ {
				xnk := x[xPtr+n-k-1]
				xbk := x[xPtr+subfrLength-n+k]
				CFirstRow[k] -= float64(xn * xnk)
				CLastRow[k] -= float64(xend * xbk)
				Atmp := Af[k]
				tmp1 += float64(xnk) * Atmp
				tmp2 += float64(xbk) * Atmp
			}

			for k := 0; k <= n; k++ {
				xnk := x[xPtr+n-k]
				xbk := x[xPtr+subfrLength-n+k-1]
				CAf[k] -= tmp1 * float64(xnk)
				CAb[k] -= tmp2 * float64(xbk)
			}
		}

		tmp1 := CFirstRow[n]
		tmp2 := CLastRow[n]
		for k := 0; k < n; k++ {
			Atmp := Af[k]
			tmp1 += CLastRow[n-k-1] * Atmp
			tmp2 += CFirstRow[n-k-1] * Atmp
		}
		CAf[n+1] = tmp1
		CAb[n+1] = tmp2

		num := CAb[n+1]
		nrgB := CAb[0]
		nrgF := CAf[0]
		for k := 0; k < n; k++ {
			Atmp := Af[k]
			num += CAb[n-k] * Atmp
			nrgB += CAb[k+1] * Atmp
			nrgF += CAf[k+1] * Atmp
		}
		if nrgF <= 0 || nrgB <= 0 {
			break
		}

		rc := -2.0 * num / (nrgF + nrgB)
		tmp1 = invGain * (1.0 - rc*rc)
		if tmp1 <= minInvGain {
			rc = math.Sqrt(1.0 - minInvGain/invGain)
			if num > 0 {
				rc = -rc
			}
			invGain = minInvGain
			reachedMaxGain = true
		} else {
			invGain = tmp1
		}

		for k := 0; k < (n+1)>>1; k++ {
			tmp1 = Af[k]
			tmp2 = Af[n-k-1]
			Af[k] = tmp1 + rc*tmp2
			Af[n-k-1] = tmp2 + rc*tmp1
		}
		Af[n] = rc

		if reachedMaxGain {
			for k := n + 1; k < order; k++ {
				Af[k] = 0
			}
			break
		}

		for k := 0; k <= n+1; k++ {
			tmp1 = CAf[k]
			CAf[k] += rc * CAb[n-k+1]
			CAb[n-k+1] += rc * tmp1
		}
	}

	var nrgF float64
	if reachedMaxGain {
		adjustedC0 := C0
		for s := 0; s < nbSubfr; s++ {
			start := s * subfrLength
			if start+order > totalLen {
				break
			}
			adjustedC0 -= energyF32(x[start:start+order], order)
		}
		nrgF = adjustedC0 * invGain
	} else {
		nrgF = CAf[0]
		tmp1 := 1.0
		for k := 0; k < order; k++ {
			Atmp := Af[k]
			nrgF += CAf[k+1] * Atmp
			tmp1 += Atmp * Atmp
		}
		nrgF -= condFac * C0 * tmp1
	}

	const pcmScaleSq = 32768.0 * 32768.0
	e.lastTotalEnergy = C0 * pcmScaleSq
	e.lastInvGain = invGain
	e.lastNumSamples = totalLen

	// Match libopus: A[k] = (silk_float)(-Af[k]) and return (silk_float)nrg_f.
	// Truncate to float32 precision to match libopus silk_float output.
	A := ensureFloat64Slice(&e.scratchBurgResult, order)
	for k := 0; k < order; k++ {
		A[k] = float64(float32(-Af[k]))
	}

	return A, float64(float32(nrgF))
}

// FindLPCWithInterpolation performs LPC analysis with NLSF interpolation search.
// This matches libopus silk_find_LPC_FLP behavior.
//
// Returns: NLSF Q15 coefficients and interpolation index (0-4)
func (e *Encoder) FindLPCWithInterpolation(x []float32, prevNLSFQ15 []int16, useInterp, firstFrame bool, nbSubfr int) ([]int16, int) {
	order := e.lpcOrder

	// Default: no interpolation
	interpCoef := 4

	// Convert to float64
	xF64 := make([]float64, len(x))
	for i := range x {
		xF64[i] = float64(x[i])
	}

	// For Burg analysis, we need subfrLength such that subfrLength*nbSubfr <= len(x)
	// The subfrLength already includes space for order preceding samples in libopus design
	// but for simplicity, we use the basic subframe length here
	subfrLength := len(x) / nbSubfr
	if subfrLength < order+1 {
		// Not enough samples per subframe
		nlsfQ15 := make([]int16, order)
		// Return white spectrum
		for i := 0; i < order; i++ {
			nlsfQ15[i] = int16((i + 1) * 32767 / (order + 1))
		}
		return nlsfQ15, 4
	}

	// Burg AR analysis for full frame
	// burgModifiedFLP now returns float32-precision values matching libopus
	a, resNrgF64 := burgModifiedFLP(xF64, minInvGain, subfrLength, nbSubfr, order)
	resNrg := float32(resNrgF64)

	// Check for NLSF interpolation
	if useInterp && !firstFrame && nbSubfr == maxNbSubfr {
		// Compute optimal solution for last 10ms (half the subframes)
		halfOffset := (maxNbSubfr / 2) * subfrLength
		if halfOffset+subfrLength*(maxNbSubfr/2) <= len(xF64) {
			_, resNrgLastF64 := burgModifiedFLP(xF64[halfOffset:], minInvGain, subfrLength, maxNbSubfr/2, order)
			resNrg -= float32(resNrgLastF64)
		}

		// Convert to NLSF
		nlsfQ15 := a2nlsfFLP(a, order)

		// Search for best interpolation index
		// Match libopus: res_nrg, res_nrg_2nd, res_nrg_interp are all silk_float (float32)
		resNrg2nd := float32(math.MaxFloat32)
		bestResNrg := resNrg

		// For interpolation search, we need enough signal
		analyzeLen := 2 * subfrLength
		if analyzeLen <= len(xF64) {
			for k := 3; k >= 0; k-- {
				// Interpolate NLSF for first half
				nlsf0Q15 := make([]int16, order)
				interpolateNLSF(nlsf0Q15, prevNLSFQ15, nlsfQ15, k, order)

				// Convert to LPC
				aTmp := make([]float64, order)
				nlsfToLPCFloat(aTmp, nlsf0Q15, order)

				// Calculate residual energy with interpolation
				lpcRes := make([]float64, analyzeLen)
				lpcAnalysisFilterFLP(lpcRes, aTmp, xF64, analyzeLen, order)

				// Compute energy of residual (excluding initial order samples)
				// Match libopus: res_nrg_interp = (silk_float)( energy0 + energy1 )
				nrgAccum := energyF64(lpcRes[order:], subfrLength-order)
				if subfrLength+order < analyzeLen {
					nrgAccum += energyF64(lpcRes[subfrLength:], silkMinInt(subfrLength-order, analyzeLen-subfrLength))
				}
				resNrgInterp := float32(nrgAccum)

				if resNrgInterp < bestResNrg {
					bestResNrg = resNrgInterp
					interpCoef = k
				} else if resNrgInterp > resNrg2nd {
					break
				}
				resNrg2nd = resNrgInterp
			}
		}

		if interpCoef == 4 {
			return nlsfQ15, interpCoef
		}
	}

	// Convert LPC to NLSF
	nlsfQ15 := a2nlsfFLP(a, order)
	return nlsfQ15, interpCoef
}

// interpolateNLSF interpolates between two NLSF vectors.
// interpCoef: 0-4 (weight for curNLSF is interpCoef/4).
func interpolateNLSF(out, prevNLSF, curNLSF []int16, interpCoef, order int) {
	if interpCoef == 4 {
		copy(out, curNLSF)
		return
	}

	// out = prevNLSF + ((curNLSF - prevNLSF) * interpCoef) >> 2
	for i := 0; i < order; i++ {
		diff := int32(curNLSF[i]) - int32(prevNLSF[i])
		out[i] = int16(int32(prevNLSF[i]) + (int32(interpCoef) * diff >> 2))
	}
}

// nlsfToLPCFloat converts NLSF Q15 to LPC float coefficients.
// This is a simplified version for interpolation search.
func nlsfToLPCFloat(a []float64, nlsfQ15 []int16, order int) {
	// Convert Q15 NLSF to cosines
	cos := make([]float64, order)
	for i := 0; i < order; i++ {
		// Linear interpolation in cosine table
		idx := int(nlsfQ15[i]) >> 8
		if idx > 127 {
			idx = 127
		}
		if idx < 0 {
			idx = 0
		}
		frac := float64(nlsfQ15[i]&0xFF) / 256.0

		c0 := float64(silk_LSFCosTab_FIX_Q12[idx]) / 4096.0
		c1 := float64(silk_LSFCosTab_FIX_Q12[idx+1]) / 4096.0
		cos[i] = c0 + (c1-c0)*frac
	}

	// Build P and Q polynomials (size halfOrder+2 to avoid bounds issues)
	halfOrder := order / 2
	P := make([]float64, halfOrder+2)
	Q := make([]float64, halfOrder+2)

	P[0] = 1.0
	Q[0] = 1.0

	// Build polynomials by adding roots one at a time
	for i := 0; i < halfOrder; i++ {
		// Even root (P polynomial)
		c := cos[2*i]
		// Shift existing coefficients and subtract 2*c*x contribution
		for j := i + 1; j >= 1; j-- {
			P[j] = P[j] - 2*c*P[j-1]
			if j >= 2 {
				P[j] += P[j-2]
			}
		}

		// Odd root (Q polynomial)
		c = cos[2*i+1]
		for j := i + 1; j >= 1; j-- {
			Q[j] = Q[j] - 2*c*Q[j-1]
			if j >= 2 {
				Q[j] += Q[j-2]
			}
		}
	}

	// Combine P and Q to get LPC
	// a[k] = 0.5 * (P[k] + P[k+1] + Q[k] - Q[k+1]) for k even
	// a[k] = 0.5 * (P[k] + P[k+1] - Q[k] + Q[k+1]) for k odd
	// This matches the libopus NLSF2A pattern
	for i := 0; i < order; i++ {
		k := i / 2
		if k+1 > halfOrder {
			// Avoid out of bounds
			a[i] = 0
			continue
		}
		pSum := P[k] + P[k+1]
		qDiff := Q[k] - Q[k+1]
		if i%2 == 0 {
			a[i] = 0.5 * (pSum + qDiff)
		} else {
			a[i] = 0.5 * (pSum - qDiff)
		}
	}
}
