//go:build !gopus_fixed_point

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
