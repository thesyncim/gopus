//go:build !arm64 && !amd64

package celt

// pitchAutocorr5 computes 5 autocorrelation values (lag 0-4) with float32
// accumulation, matching libopus _celt_autocorr() for lag=4, overlap=0.
func pitchAutocorr5(lp []float64, length int, ac *[5]float64) {
	fastN := length - 4
	if fastN < 0 {
		fastN = 0
	}
	for lag := 0; lag <= 4; lag++ {
		sum := float32(0)
		for i := 0; i < fastN; i++ {
			sum += float32(lp[i]) * float32(lp[i+lag])
		}
		for i := lag + fastN; i < length; i++ {
			sum += float32(lp[i]) * float32(lp[i-lag])
		}
		ac[lag] = float64(sum)
	}
}
