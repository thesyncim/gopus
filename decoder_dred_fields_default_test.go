//go:build !gopus_dred && !gopus_extra_controls

package gopus

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildDecoderDREDFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(decoderDREDFields{}); got != 0 {
		t.Fatalf("decoderDREDFields size=%d want 0", got)
	}
}
