// Package cpufeat exposes architecture feature flags. The arm64 initializers
// populate ARM64: Darwin queries the listed optional extensions, while other
// arm64 targets mark only baseline ASIMD support. This package has no amd64
// probe, so AMD64 remains zero-valued.
package cpufeat

// AMD64Features holds optional amd64 instruction-set flags. No amd64
// initializer populates these fields, so the package-level AMD64 value remains
// zero-valued.
type AMD64Features struct {
	HasAVX2 bool
	HasFMA  bool
}

// ARM64Features holds the arm64 (AArch64) instruction-set flags detected by the
// package's architecture-specific initializer. On non-Darwin arm64, only
// HasASIMD is set; the optional extension flags are not probed there.
type ARM64Features struct {
	HasASIMD     bool
	HasDotProd   bool
	HasFCMA      bool
	HasFHM       bool
	HasBF16      bool
	HasI8MM      bool
	HasSME       bool
	HasSMEF64F64 bool
}

// AMD64 is the package's amd64 feature view. It remains zero-valued because no
// amd64 initializer populates it.
var AMD64 AMD64Features

// ARM64 is populated at package initialization on arm64 builds. It is
// zero-valued on other architectures; non-Darwin arm64 builds set only
// HasASIMD.
var ARM64 ARM64Features
