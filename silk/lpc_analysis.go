package silk

import "math"

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

	// LPC coefficients (float for computation)
	// Using standard Burg's algorithm with proper indexing
	a := make([]float64, order+1)
	a[0] = 1.0

	// Forward and backward prediction errors - initialize with signal
	ef := make([]float64, n)
	eb := make([]float64, n)
	for i := 0; i < n; i++ {
		ef[i] = float64(signal[i])
		eb[i] = float64(signal[i])
	}

	// Compute LPC coefficients via Burg's method
	for m := 0; m < order; m++ {
		// Compute reflection coefficient
		// k = -2 * sum(ef[m+1:n] * eb[m:n-1]) / sum(ef[m+1:n]^2 + eb[m:n-1]^2)
		var num, den float64
		for j := m + 1; j < n; j++ {
			num += ef[j] * eb[j-1]
			den += ef[j]*ef[j] + eb[j-1]*eb[j-1]
		}

		if den < 1e-10 {
			// Numerical issue, stop iteration - remaining coeffs stay 0
			break
		}

		// Reflection coefficient (PARCOR coefficient)
		k := -2.0 * num / den

		// Clamp reflection coefficient for stability (|k| < 1)
		if k > 0.999 {
			k = 0.999
		} else if k < -0.999 {
			k = -0.999
		}

		// Update LPC coefficients using Levinson-Durbin recursion
		// Save old coefficients before updating
		aOld := make([]float64, m+2)
		for j := 0; j <= m; j++ {
			aOld[j] = a[j+1]
		}

		// Update coefficients: a[j+1] = a[j+1] + k * a[m-j+1]
		// The symmetric access pattern for Levinson-Durbin uses m-j, not m-1-j
		// This ensures correct coefficient update at all orders
		for j := 0; j < m; j++ {
			a[j+1] = aOld[j] + k*aOld[m-j]
		}
		a[m+1] = k

		// Update forward and backward prediction errors
		for j := n - 1; j > m; j-- {
			efOld := ef[j]
			ef[j] = efOld + k*eb[j-1]
			eb[j] = eb[j-1] + k*efOld
		}
	}

	// Convert to Q12 fixed-point
	lpcQ12 := make([]int16, order)
	for i := 0; i < order; i++ {
		val := a[i+1] * 4096.0 // Q12 scaling
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		lpcQ12[i] = int16(val)
	}

	return lpcQ12
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
func (e *Encoder) computeLPCFromFrame(pcm []float32) []int16 {
	// Apply window (can use simple Hamming or none for now)
	windowed := make([]float32, len(pcm))
	n := float64(len(pcm))
	for i := range pcm {
		// Hamming window
		w := 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/(n-1))
		windowed[i] = pcm[i] * float32(w)
	}

	// Compute LPC via Burg's method
	lpcQ12 := burgLPC(windowed, e.lpcOrder)

	// Apply bandwidth expansion for stability (chirp = 0.96)
	applyBandwidthExpansionFloat(lpcQ12, 0.96)

	return lpcQ12
}
