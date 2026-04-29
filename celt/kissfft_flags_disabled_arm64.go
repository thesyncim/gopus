//go:build arm64

package celt

const kissFFTM1FastPathEnabled = true

const kissFFTNoFMAMulEnabled = false

const kissFFTFMALikeEnabled = true

const kissFFTDFTFallbackEnabled = false

// The 120-point FFT feeds 5 ms CELT MDCT. On arm64, libopus' scalar C path
// rounds this size differently from the generic FMAlike butterfly assembly.
const kissFFTCOrder120Enabled = true
