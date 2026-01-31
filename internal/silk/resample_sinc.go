package silk

import (
	"math"
)

// sincResampler implements a windowed sinc resampler for SILK output.
// This provides much higher quality upsampling than linear interpolation.
type sincResampler struct {
	// Filter taps for each fractional phase
	filterTaps int
	phases     int
	filter     []float32

	// State buffer for filter history
	history   []float32
	historyMu int // history buffer position
}

// newSincResampler creates a sinc resampler for the given upsampling ratio.
func newSincResampler(upsampleRatio int) *sincResampler {
	// Use 8 taps per side (16 total) - good quality/speed tradeoff
	taps := 16
	phases := upsampleRatio

	r := &sincResampler{
		filterTaps: taps,
		phases:     phases,
		filter:     make([]float32, taps*phases),
		history:    make([]float32, taps),
	}

	// Generate windowed sinc filter coefficients
	// Using Kaiser window for good sidelobe suppression
	r.generateFilter()

	return r
}

// generateFilter creates the windowed sinc filter coefficients.
func (r *sincResampler) generateFilter() {
	halfTaps := r.filterTaps / 2

	// Kaiser window parameter (beta=6 gives good quality)
	beta := 6.0

	for phase := 0; phase < r.phases; phase++ {
		offset := float64(phase) / float64(r.phases)

		for tap := 0; tap < r.filterTaps; tap++ {
			// Sinc function centered at the fractional phase
			x := float64(tap-halfTaps) + offset
			var sinc float64
			if math.Abs(x) < 1e-10 {
				sinc = 1.0
			} else {
				sinc = math.Sin(math.Pi*x) / (math.Pi * x)
			}

			// Kaiser window
			n := float64(tap)/float64(r.filterTaps-1)*2.0 - 1.0
			window := kaiserWindow(n, beta)

			r.filter[phase*r.filterTaps+tap] = float32(sinc * window)
		}

		// Normalize this phase's coefficients
		var sum float32
		for tap := 0; tap < r.filterTaps; tap++ {
			sum += r.filter[phase*r.filterTaps+tap]
		}
		if sum > 0 {
			for tap := 0; tap < r.filterTaps; tap++ {
				r.filter[phase*r.filterTaps+tap] /= sum
			}
		}
	}
}

// kaiserWindow computes the Kaiser window function.
func kaiserWindow(x, beta float64) float64 {
	if x <= -1 || x >= 1 {
		return 0
	}
	return bessel0(beta*math.Sqrt(1-x*x)) / bessel0(beta)
}

// bessel0 computes the modified Bessel function of the first kind, order 0.
func bessel0(x float64) float64 {
	// Series expansion
	sum := 1.0
	term := 1.0
	for k := 1; k < 50; k++ {
		term *= (x / (2 * float64(k))) * (x / (2 * float64(k)))
		sum += term
		if term < 1e-12*sum {
			break
		}
	}
	return sum
}

// Reset clears the resampler state.
func (r *sincResampler) Reset() {
	for i := range r.history {
		r.history[i] = 0
	}
	r.historyMu = 0
}

// Process resamples the input samples to the output rate.
func (r *sincResampler) Process(input []float32) []float32 {
	output := make([]float32, len(input)*r.phases)

	// Extend input with zero padding for filter lookback
	halfTaps := r.filterTaps / 2
	extended := make([]float32, len(input)+r.filterTaps)
	copy(extended[halfTaps:], input)

	for i := 0; i < len(input); i++ {
		// Output one sample for each phase
		for phase := 0; phase < r.phases; phase++ {
			var sum float32
			for tap := 0; tap < r.filterTaps; tap++ {
				idx := i + tap
				if idx >= 0 && idx < len(extended) {
					sum += extended[idx] * r.filter[phase*r.filterTaps+tap]
				}
			}
			outIdx := i*r.phases + phase
			if outIdx < len(output) {
				output[outIdx] = sum
			}
		}
	}

	return output
}

// upsampleTo48kSinc resamples SILK output to 48kHz using sinc interpolation.
// This provides much better quality than linear interpolation.
func upsampleTo48kSinc(samples []float32, srcRate int) []float32 {
	if srcRate == 48000 {
		return samples
	}

	factor := 48000 / srcRate
	if factor < 1 || factor > 6 {
		panic("upsampleTo48kSinc: invalid source rate")
	}

	if len(samples) == 0 {
		return nil
	}

	resampler := newSincResampler(factor)
	return resampler.Process(samples)
}
