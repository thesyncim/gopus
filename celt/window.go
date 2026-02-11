package celt

import "math"

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

// windowBuffer120 contains precomputed Vorbis window values for overlap=Overlap.
var windowBuffer120 [Overlap]float64
var windowBuffer120F32 [Overlap]float32
var windowBuffer120Sq [Overlap]float64

// libopus static_modes_float.h window120 table (float build).
// Using exact constants avoids tiny startup-time trig differences that can
// accumulate into parity drift in prefilter/MDCT paths.
var window120StaticF32 = [Overlap]float32{
	6.7286966e-05, 0.00060551348, 0.0016815970, 0.0032947962, 0.0054439943,
	0.0081276923, 0.011344001, 0.015090633, 0.019364886, 0.024163635,
	0.029483315, 0.035319905, 0.041668911, 0.048525347, 0.055883718,
	0.063737999, 0.072081616, 0.080907428, 0.090207705, 0.099974111,
	0.11019769, 0.12086883, 0.13197729, 0.14351214, 0.15546177,
	0.16781389, 0.18055550, 0.19367290, 0.20715171, 0.22097682,
	0.23513243, 0.24960208, 0.26436860, 0.27941419, 0.29472040,
	0.31026818, 0.32603788, 0.34200931, 0.35816177, 0.37447407,
	0.39092462, 0.40749142, 0.42415215, 0.44088423, 0.45766484,
	0.47447104, 0.49127978, 0.50806798, 0.52481261, 0.54149077,
	0.55807973, 0.57455701, 0.59090049, 0.60708841, 0.62309951,
	0.63891306, 0.65450896, 0.66986776, 0.68497077, 0.69980010,
	0.71433873, 0.72857055, 0.74248043, 0.75605425, 0.76927895,
	0.78214257, 0.79463430, 0.80674445, 0.81846456, 0.82978733,
	0.84070669, 0.85121779, 0.86131698, 0.87100183, 0.88027111,
	0.88912479, 0.89756398, 0.90559094, 0.91320904, 0.92042270,
	0.92723738, 0.93365955, 0.93969656, 0.94535671, 0.95064907,
	0.95558353, 0.96017067, 0.96442171, 0.96834849, 0.97196334,
	0.97527906, 0.97830883, 0.98106616, 0.98356480, 0.98581869,
	0.98784191, 0.98964856, 0.99125274, 0.99266849, 0.99390969,
	0.99499004, 0.99592297, 0.99672162, 0.99739874, 0.99796667,
	0.99843728, 0.99882195, 0.99913147, 0.99937606, 0.99956527,
	0.99970802, 0.99981248, 0.99988613, 0.99993565, 0.99996697,
	0.99998518, 0.99999457, 0.99999859, 0.99999982, 1.0000000,
}

// windowBuffer240 contains precomputed window for 5ms frames.
var windowBuffer240 [240]float64
var windowBuffer240F32 [240]float32
var windowBuffer240Sq [240]float64

// windowBuffer480 contains precomputed window for 10ms frames.
var windowBuffer480 [480]float64
var windowBuffer480F32 [480]float32
var windowBuffer480Sq [480]float64

// windowBuffer960 contains precomputed window for 20ms frames.
var windowBuffer960 [960]float64
var windowBuffer960F32 [960]float32
var windowBuffer960Sq [960]float64

func init() {
	// Precompute window buffers for all frame sizes
	// The window is defined over the overlap region.

	// For overlap=Overlap (2.5ms at 48kHz)
	for i := 0; i < Overlap; i++ {
		windowBuffer120F32[i] = window120StaticF32[i]
		windowBuffer120[i] = float64(windowBuffer120F32[i])
		windowBuffer120Sq[i] = windowBuffer120[i] * windowBuffer120[i]
	}

	// For overlap=240 (2.5ms at 96kHz)
	for i := 0; i < 240; i++ {
		windowBuffer240[i] = VorbisWindow(i, 240)
		windowBuffer240F32[i] = float32(windowBuffer240[i])
		windowBuffer240Sq[i] = windowBuffer240[i] * windowBuffer240[i]
	}

	// For overlap=480
	for i := 0; i < 480; i++ {
		windowBuffer480[i] = VorbisWindow(i, 480)
		windowBuffer480F32[i] = float32(windowBuffer480[i])
		windowBuffer480Sq[i] = windowBuffer480[i] * windowBuffer480[i]
	}

	// For overlap=960
	for i := 0; i < 960; i++ {
		windowBuffer960[i] = VorbisWindow(i, 960)
		windowBuffer960F32[i] = float32(windowBuffer960[i])
		windowBuffer960Sq[i] = windowBuffer960[i] * windowBuffer960[i]
	}
}

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
