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
// Converts Q16 to float32 and calls computeLogGainIndex which uses binary search
func ComputeLogGainIndexQ16Export(gainQ16 int32) int {
	// Convert Q16 back to float: gainQ16 / 65536.0
	gain := float32(gainQ16) / 65536.0
	return computeLogGainIndex(gain)
}

// ComputeLogGainIndexExport exposes the float gain quantization for testing
func ComputeLogGainIndexExport(gain float32) int {
	return computeLogGainIndex(gain)
}
