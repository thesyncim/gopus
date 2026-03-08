package cpufeat

type AMD64Features struct {
	HasAVX2 bool
	HasFMA  bool
}

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

var AMD64 AMD64Features
var ARM64 ARM64Features
