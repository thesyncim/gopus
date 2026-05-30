//go:build !gopus_fixedpoint

package silk

import (
	"testing"
	"unsafe"
)

// TestSILKEncoderFixedFieldsZeroSizeDefault enforces the zero-cost contract:
// the integer SILK encode state embedded in the Encoder must be zero-size in
// the default (float) build, keeping the Encoder struct byte-unchanged.
func TestSILKEncoderFixedFieldsZeroSizeDefault(t *testing.T) {
	if got := unsafe.Sizeof(silkEncoderFixedFields{}); got != 0 {
		t.Fatalf("silkEncoderFixedFields size=%d want 0", got)
	}
}
