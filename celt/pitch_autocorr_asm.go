//go:build arm64 || amd64

package celt

// pitchAutocorr5 computes 5 autocorrelation values (lag 0-4) with float32
// accumulation, matching libopus _celt_autocorr() for lag=4, overlap=0.
//
//go:noescape
func pitchAutocorr5(lp []float64, length int, ac *[5]float64)
