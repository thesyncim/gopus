//go:build !gopus_fixedpoint

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
