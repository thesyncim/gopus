package silk

// Exports for CGO comparison tests.
// These functions expose internal implementations for validation against libopus.

// SilkLin2LogExport exposes silkLin2Log for testing
func SilkLin2LogExport(in int32) int32 {
	return silkLin2Log(in)
}

// SilkLog2LinExport exposes silkLog2Lin for testing
func SilkLog2LinExport(in int32) int32 {
	return silkLog2Lin(in)
}

// SilkSMULWBExport exposes silkSMULWB for testing
func SilkSMULWBExport(a, b int32) int32 {
	return silkSMULWB(a, b)
}

// ComputeLogGainIndexQ16Export exposes the Q16 gain quantization for testing
func ComputeLogGainIndexQ16Export(gainQ16 int32) int {
	return computeLogGainIndexQ16(gainQ16)
}

// ComputeLogGainIndexExport exposes the float gain quantization for testing
func ComputeLogGainIndexExport(gain float32) int {
	return computeLogGainIndex(gain)
}
