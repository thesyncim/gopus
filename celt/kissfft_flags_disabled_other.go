//go:build !gopus_tmp_env && !arm64

package celt

const kissFFTM1FastPathEnabled = true

const kissFFTNoFMAMulEnabled = false

const kissFFTFMALikeEnabled = false

const kissFFTDFTFallbackEnabled = false
