//go:build !gopus_qext

package encoder

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildQEXTFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(encoderQEXTFields{}); got != 0 {
		t.Fatalf("encoderQEXTFields size=%d want 0", got)
	}
}
