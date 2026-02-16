//go:build !arm64 && !amd64

package celt

// transientEnergyPairs computes energy of sample pairs.
func transientEnergyPairs(tmp []float64, x2out []float32, len2 int) float64 {
	var mean float32
	for i := 0; i < len2; i++ {
		t0 := float32(tmp[2*i])
		t1 := float32(tmp[2*i+1])
		x2 := t0*t0 + t1*t1
		x2out[i] = x2
		mean += x2
	}
	return float64(mean)
}
