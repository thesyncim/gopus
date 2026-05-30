//go:build !gopus_fixedpoint

package multistream

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildStreamFixedFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(streamFixedFields{}); got != 0 {
		t.Fatalf("streamFixedFields size = %d, want 0", got)
	}
}
