//go:build !gopus_dred && !gopus_extra_controls
// +build !gopus_dred,!gopus_extra_controls

package encoder

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildDREDFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(encoderDREDFields{}); got != 0 {
		t.Fatalf("encoderDREDFields size=%d want 0", got)
	}
}
