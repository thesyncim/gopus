package silk

import "math"

// lpcToLSFEncode converts LPC coefficients to LSF (Line Spectral Frequencies).
// Uses the Chebyshev polynomial method per Kabal & Ramachandran 1986.
//
// lpcQ12: LPC coefficients in Q12 format
// Returns: LSF values in Q15 format [0, 32767] representing [0, pi]
func lpcToLSFEncode(lpcQ12 []int16) []int16 {
	order := len(lpcQ12)
	if order == 0 {
		return nil
	}

	// Convert Q12 to float for computation
	lpc := make([]float64, order)
	for i := 0; i < order; i++ {
		lpc[i] = float64(lpcQ12[i]) / 4096.0
	}

	// Construct symmetric polynomials P(z) and Q(z)
	// P(z) = A(z) + z^(-order-1) * A(z^-1)  (sum polynomial)
	// Q(z) = A(z) - z^(-order-1) * A(z^-1)  (difference polynomial)
	halfOrder := order / 2

	// P and Q polynomial coefficients (after factoring out 1+z^-1 and 1-z^-1)
	p := make([]float64, halfOrder+1)
	q := make([]float64, halfOrder+1)

	// Build P: a[k] + a[order-1-k]
	// Build Q: a[k] - a[order-1-k]
	p[0] = 1.0
	q[0] = 1.0
	for i := 0; i < halfOrder; i++ {
		p[i+1] = lpc[i] + lpc[order-1-i]
		q[i+1] = lpc[i] - lpc[order-1-i]
	}

	// Cumulative sum for P
	for i := 1; i <= halfOrder; i++ {
		p[i] += p[i-1]
	}
	// Cumulative difference for Q
	for i := 1; i <= halfOrder; i++ {
		q[i] -= q[i-1]
	}

	// Find roots by searching for sign changes in [0, pi]
	lsfFloat := make([]float64, order)
	lsfIdx := 0

	// Search resolution
	const numPoints = 1024
	const step = math.Pi / float64(numPoints)

	prevP := evalChebyshev(p, 1.0) // cos(0) = 1
	prevQ := evalChebyshev(q, 1.0)
	prevW := 0.0

	for i := 1; i <= numPoints && lsfIdx < order; i++ {
		w := float64(i) * step
		x := math.Cos(w)

		currP := evalChebyshev(p, x)
		currQ := evalChebyshev(q, x)

		// Check for sign change in P (even-indexed LSFs)
		if lsfIdx%2 == 0 && prevP*currP < 0 {
			// Bisection to refine root
			root := bisectRoot(p, prevW, w, evalChebyshev)
			lsfFloat[lsfIdx] = root
			lsfIdx++
		}

		// Check for sign change in Q (odd-indexed LSFs)
		if lsfIdx%2 == 1 && prevQ*currQ < 0 {
			root := bisectRoot(q, prevW, w, evalChebyshev)
			lsfFloat[lsfIdx] = root
			lsfIdx++
		}

		prevP = currP
		prevQ = currQ
		prevW = w
	}

	// If we didn't find all roots, fill with evenly spaced values
	if lsfIdx < order {
		for i := lsfIdx; i < order; i++ {
			lsfFloat[i] = math.Pi * float64(i+1) / float64(order+1)
		}
	}

	// Convert to Q15 format [0, 32767]
	lsfQ15 := make([]int16, order)
	for i := 0; i < order; i++ {
		// Map [0, pi] to [0, 32767]
		val := lsfFloat[i] * 32767.0 / math.Pi
		if val < 0 {
			val = 0
		}
		if val > 32767 {
			val = 32767
		}
		lsfQ15[i] = int16(val)
	}

	// Ensure strict ordering (bubble sort if needed)
	ensureLSFOrdering(lsfQ15)

	return lsfQ15
}

// evalChebyshev evaluates polynomial at x using Chebyshev recursion.
func evalChebyshev(coef []float64, x float64) float64 {
	if len(coef) == 0 {
		return 0
	}
	if len(coef) == 1 {
		return coef[0]
	}

	// Clenshaw's recurrence for Chebyshev evaluation
	var b0, b1 float64
	for i := len(coef) - 1; i >= 0; i-- {
		b2 := b1
		b1 = b0
		b0 = 2*x*b1 - b2 + coef[i]
	}
	return b0 - x*b1
}

// bisectRoot finds root of poly in [lo, hi] using bisection.
func bisectRoot(poly []float64, lo, hi float64, evalFunc func([]float64, float64) float64) float64 {
	const maxIter = 20
	const tol = 1e-8

	flo := evalFunc(poly, math.Cos(lo))

	for iter := 0; iter < maxIter; iter++ {
		mid := (lo + hi) / 2
		if hi-lo < tol {
			return mid
		}

		fmid := evalFunc(poly, math.Cos(mid))
		if flo*fmid < 0 {
			hi = mid
		} else {
			lo = mid
			flo = fmid
		}
	}

	return (lo + hi) / 2
}

// ensureLSFOrdering ensures LSF values are strictly increasing.
func ensureLSFOrdering(lsf []int16) {
	// Minimum spacing between adjacent LSF (about 100 Hz in Q15)
	const minSpacing = 100

	for i := 1; i < len(lsf); i++ {
		if lsf[i] <= lsf[i-1]+minSpacing {
			lsf[i] = lsf[i-1] + minSpacing
		}
	}

	// Clamp to valid range
	for i := range lsf {
		if lsf[i] > 32600 {
			lsf[i] = 32600
		}
	}
}
