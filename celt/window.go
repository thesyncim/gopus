package celt

import "math"

//go:generate go run ../tools/gen_window_tables.go -out window_tables_static.go

// Vorbis window implementation for CELT overlap-add synthesis.
// CELT defines the window over the overlap region (length = overlap):
//   w[i] = sin(0.5*pi * sin(0.5*pi*(i+0.5)/overlap)^2)
// for i in [0, overlap). This matches libopus celt/modes.c.
//
// The window is power-complementary:
//   w[i]^2 + w[overlap-1-i]^2 = 1
// which preserves energy during overlap-add.
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/modes.c

// VorbisWindow computes the CELT Vorbis window value at position i for the
// given overlap length. This matches libopus's window generation.
//
// This window is:
// - Power-complementary: w[i]^2 + w[overlap-1-i]^2 = 1
// - Smooth: continuous first derivative
// - Good spectral properties: low sidelobe levels
//
// Parameters:
//   - i: position in window (0 to overlap-1)
//   - overlap: window length (overlap region)
//
// Returns: window value in [0, 1]
func VorbisWindow(i, overlap int) float64 {
	if overlap <= 0 {
		return 0
	}
	x := float64(i) + 0.5
	sinArg := 0.5 * math.Pi * x / float64(overlap)
	s := math.Sin(sinArg)
	return math.Sin(0.5 * math.Pi * s * s)
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
			window[i] = VorbisWindow(i, overlap)
		}
		return window
	}
}

// GetWindowBufferF32 returns the precomputed float32 window buffer for the given overlap size.
// Returns a freshly computed float32 buffer for non-standard sizes.
func GetWindowBufferF32(overlap int) []float32 {
	switch overlap {
	case 120:
		return windowBuffer120F32[:]
	case 240:
		return windowBuffer240F32[:]
	case 480:
		return windowBuffer480F32[:]
	case 960:
		return windowBuffer960F32[:]
	default:
		window := make([]float32, overlap)
		for i := 0; i < overlap; i++ {
			window[i] = float32(VorbisWindow(i, overlap))
		}
		return window
	}
}

// GetWindowSquareBuffer returns precomputed w[i]^2 values for the overlap window.
// This avoids recomputing window[i]*window[i] inside hot comb-filter loops.
func GetWindowSquareBuffer(overlap int) []float64 {
	switch overlap {
	case 120:
		return windowBuffer120Sq[:]
	case 240:
		return windowBuffer240Sq[:]
	case 480:
		return windowBuffer480Sq[:]
	case 960:
		return windowBuffer960Sq[:]
	default:
		windowSq := make([]float64, overlap)
		for i := 0; i < overlap; i++ {
			w := VorbisWindow(i, overlap)
			windowSq[i] = w * w
		}
		return windowSq
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
		w := VorbisWindow(i, overlap)
		energy += w * w
	}
	// The other half contributes equally
	return 2 * energy
}

// GetWindow returns the standard CELT overlap window (120 samples).
// This is used for gain fading in hybrid mode to ensure smooth transitions.
// Returns nil if the window is not available.
func GetWindow() []float64 {
	return GetWindowBuffer(Overlap)
}
