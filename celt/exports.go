package celt

// ExportedOpPVQSearch exposes opPVQSearch for testing.
func ExportedOpPVQSearch(x []float64, k int) ([]int, float64) {
	return opPVQSearch(x, k)
}

// BitsToPulsesExport exposes bitsToPulses for testing.
func BitsToPulsesExport(band, lm, bitsQ3 int) int {
	return bitsToPulses(band, lm, bitsQ3)
}

// GetPulsesExport exposes getPulses for testing.
func GetPulsesExport(q int) int {
	return getPulses(q)
}

// PulsesToBitsExport exposes pulsesToBits for testing.
func PulsesToBitsExport(band, lm, pulses int) int {
	return pulsesToBits(band, lm, pulses)
}

// ExpRotationExport exposes expRotation for testing.
func ExpRotationExport(x []float64, length, dir, stride, k, spread int) {
	expRotation(x, length, dir, stride, k, spread)
}

// OpPVQSearchExport exposes opPVQSearch for testing (same as ExportedOpPVQSearch).
func OpPVQSearchExport(x []float64, k int) ([]int, float64) {
	return opPVQSearch(x, k)
}
