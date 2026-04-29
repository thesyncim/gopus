//go:build !arm64

package celt

const kissFFTM1FastPathEnabled = true

const kissFFTNoFMAMulEnabled = false

const kissFFTFMALikeEnabled = false

const kissFFTDFTFallbackEnabled = false

const kissFFTCOrder120Enabled = false
