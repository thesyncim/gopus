package encoder

// round32 is an explicit float32 rounding boundary. On targets with fused
// multiply-add the Go compiler may contract a product into a later addition,
// also across statements, but never across this conversion. The C compilers
// of the paired libopus builds contract only within one expression, so a
// product that libopus computes in a statement of its own is rounded here
// before it is added.
func round32(x float32) float32 { return float32(x) }

// fma32 is a*b + c. It compiles to one fused multiply-add where Go contracts
// (arm64) and to a rounded product plus c elsewhere, like the C compiler of the
// paired libopus build. clang contracts C's a*b + c*d into
// fma32(a, b, round32(c*d)); Go on its own would fuse the right product.
func fma32(a, b, c float32) float32 { return a*b + c }
