package celt

import "math"

// Vorbis window implementation for CELT overlap-add synthesis.
// The Vorbis window is a power-complementary window used in CELT to ensure
// perfect reconstruction when combining overlapping frames.
//
// The window satisfies: w[i]^2 + w[n-1-i]^2 = 1 (power complementary)
// This ensures that overlap-add reconstruction preserves energy.
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/celt.c

// WindowBufferSize is the precomputed window size for CELT overlap.
// This matches the Overlap constant (120 samples at 48kHz = 2.5ms).
const WindowBufferSize = 120

// windowBuffer120 contains precomputed Vorbis window values for overlap=120.
// These are computed for a window of size 240 (2*overlap), and we store
// the first 120 values since the window is symmetric.
var windowBuffer120 [WindowBufferSize]float64

// windowBuffer240 contains precomputed window for 5ms frames.
var windowBuffer240 [240]float64

// windowBuffer480 contains precomputed window for 10ms frames.
var windowBuffer480 [480]float64

// windowBuffer960 contains precomputed window for 20ms frames.
var windowBuffer960 [960]float64

func init() {
	// Precompute window buffers for all frame sizes
	// The window is defined over 2*overlap samples

	// For overlap=120 (2.5ms base)
	for i := 0; i < WindowBufferSize; i++ {
		windowBuffer120[i] = VorbisWindow(i, 2*WindowBufferSize)
	}

	// For 5ms frames (240 samples, overlap=120)
	for i := 0; i < 240; i++ {
		windowBuffer240[i] = VorbisWindow(i, 480)
	}

	// For 10ms frames (480 samples, overlap=120)
	for i := 0; i < 480; i++ {
		windowBuffer480[i] = VorbisWindow(i, 960)
	}

	// For 20ms frames (960 samples, overlap=120)
	for i := 0; i < 960; i++ {
		windowBuffer960[i] = VorbisWindow(i, 1920)
	}
}

// VorbisWindow computes the Vorbis window value at position i of n.
// The Vorbis window is defined as:
//   w(i) = sin(pi/2 * sin^2(pi*(i+0.5)/n))
//
// This window is:
// - Power-complementary: w[i]^2 + w[n-1-i]^2 = 1
// - Smooth: continuous first derivative
// - Good spectral properties: low sidelobe levels
//
// Parameters:
//   - i: position in window (0 to n-1)
//   - n: total window size
//
// Returns: window value in [0, 1]
func VorbisWindow(i, n int) float64 {
	if n <= 0 {
		return 0
	}
	x := float64(i) + 0.5
	sinArg := math.Pi * x / float64(n)
	return math.Sin(math.Pi / 2.0 * math.Pow(math.Sin(sinArg), 2))
}

// GetWindowBuffer returns the precomputed window buffer for the given overlap size.
// For the standard CELT overlap of 120 samples, returns windowBuffer120.
// Returns nil if no precomputed buffer exists for the size.
func GetWindowBuffer(overlap int) []float64 {
	switch overlap {
	case 120:
		return windowBuffer120[:]
	case 240:
		return windowBuffer240[:]
	case 480:
		return windowBuffer480[:]
	case 960:
		return windowBuffer960[:]
	default:
		// Compute on the fly for non-standard sizes
		window := make([]float64, overlap)
		for i := 0; i < overlap; i++ {
			window[i] = VorbisWindow(i, 2*overlap)
		}
		return window
	}
}

// ApplyWindow applies the Vorbis window to IMDCT output.
// The window is applied to both the beginning and end overlap regions.
//
// Parameters:
//   - samples: IMDCT output (length 2*N where N is MDCT size)
//   - overlap: overlap size (typically 120 for CELT)
//
// The windowing is in-place to avoid allocation.
func ApplyWindow(samples []float64, overlap int) {
	n := len(samples)
	if n <= 0 || overlap <= 0 {
		return
	}

	// Get precomputed window or compute
	window := GetWindowBuffer(overlap)

	// Apply window to beginning (rising edge)
	for i := 0; i < overlap && i < n; i++ {
		samples[i] *= window[i]
	}

	// Apply window to end (falling edge)
	// The falling edge uses w[n-1-i] which equals w[overlap-1-i] for our half-window
	for i := 0; i < overlap && n-1-i >= 0; i++ {
		idx := n - overlap + i
		if idx >= 0 && idx < n {
			// Falling edge: use window from end
			samples[idx] *= window[overlap-1-i]
		}
	}
}

// ApplyWindowSymmetric applies window assuming symmetric IMDCT output.
// This is optimized for the CELT case where the IMDCT output has known symmetry.
func ApplyWindowSymmetric(samples []float64, overlap int) {
	ApplyWindow(samples, overlap)
}

// WindowEnergy computes the total energy of a windowed segment.
// Used for level normalization.
func WindowEnergy(overlap int) float64 {
	var energy float64
	for i := 0; i < overlap; i++ {
		w := VorbisWindow(i, 2*overlap)
		energy += w * w
	}
	// The other half contributes equally
	return 2 * energy
}
