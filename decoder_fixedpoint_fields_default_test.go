//go:build !gopus_fixed_point

package gopus

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildDecoderFixedFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(decoderFixedFields{}); got != 0 {
		t.Fatalf("decoderFixedFields size = %d, want 0", got)
	}
}
