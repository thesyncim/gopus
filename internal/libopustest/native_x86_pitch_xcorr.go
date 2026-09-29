package libopustest

// NativeX86PitchXCorrMetadataValid checks the AVX2/FMA pitch oracle's C-side
// CPU and dispatch evidence. RTCD-selected paths require arch 4 or later;
// compile-time-presumed paths are proven by their emitted dispatch mask and
// still require the actual CPU feature check.
func NativeX86PitchXCorrMetadataValid(arch, cpu, dispatch, presumed uint32) bool {
	const (
		cpuAVX2                 = uint32(2)
		cpuFMA                  = uint32(4)
		dispatchXCorrAVX2       = uint32(1)
		dispatchInnerProductSSE = uint32(2)
		requiredCPU             = cpuAVX2 | cpuFMA
		requiredDispatch        = dispatchXCorrAVX2 | dispatchInnerProductSSE
	)
	if cpu&requiredCPU != requiredCPU || dispatch&requiredDispatch != requiredDispatch ||
		presumed&^requiredDispatch != 0 || presumed&^dispatch != 0 {
		return false
	}
	return requiredDispatch&^presumed == 0 || arch >= 4
}

// NativeX86SILKInnerProductMetadataValid checks that libopus selected the
// expected SILK inner-product implementation. A compile-time AVX2 selection
// may report arch zero, while RTCD selection requires arch four or later.
func NativeX86SILKInnerProductMetadataValid(arch, implementation, presumed uint32, wantAVX2 bool) bool {
	const (
		implementationScalar = uint32(0)
		implementationAVX2   = uint32(1)
		presumedAVX2         = uint32(1)
	)
	if implementation > implementationAVX2 || presumed&^presumedAVX2 != 0 {
		return false
	}
	if wantAVX2 != (implementation == implementationAVX2) || (presumed != 0 && implementation != implementationAVX2) {
		return false
	}
	return implementation != implementationAVX2 || presumed != 0 || arch >= 4
}
