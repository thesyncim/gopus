//go:build !gopus_fixed_point

package encoder

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildFixedCELTFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(encoderFixedCELTFields{}); got != 0 {
		t.Fatalf("encoderFixedCELTFields size=%d want 0", got)
	}
}
