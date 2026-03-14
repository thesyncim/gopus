package celt

// BitsToPulsesExport exposes bitsToPulses for testing.
//
// This helper exists for tests and codec-development tooling and may change.
func BitsToPulsesExport(band, lm, bitsQ3 int) int {
	return bitsToPulses(band, lm, bitsQ3)
}

// GetPulsesExport exposes getPulses for testing.
//
// This helper exists for tests and codec-development tooling and may change.
func GetPulsesExport(q int) int {
	return getPulses(q)
}

// PulsesToBitsExport exposes pulsesToBits for testing.
//
// This helper exists for tests and codec-development tooling and may change.
func PulsesToBitsExport(band, lm, pulses int) int {
	return pulsesToBits(band, lm, pulses)
}

// ExpRotationExport exposes expRotation for testing.
//
// This helper exists for tests and codec-development tooling and may change.
func ExpRotationExport(x []float64, length, dir, stride, k, spread int) {
	expRotation(x, length, dir, stride, k, spread)
}

// OpPVQSearchExport exposes opPVQSearch for testing.
//
// This helper exists for tests and codec-development tooling and may change.
func OpPVQSearchExport(x []float64, k int) ([]int, float64) {
	return opPVQSearch(x, k)
}

// NormalizeResidualExport exposes normalizeResidual for testing.
//
// This helper exists for tests and codec-development tooling and may change.
func NormalizeResidualExport(pulses []int, gain float64, yy float64) []float64 {
	return normalizeResidual(pulses, gain, yy)
}

// NormalizeResidualIntoExport exposes normalizeResidualInto for testing.
//
// This helper exists for tests and codec-development tooling and may change.
func NormalizeResidualIntoExport(out []float64, pulses []int, gain float64, yy float64) {
	normalizeResidualInto(out, pulses, gain, yy)
}
