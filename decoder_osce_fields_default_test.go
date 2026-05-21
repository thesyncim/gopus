//go:build !gopus_dred && !gopus_extra_controls

package gopus

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildDecoderOSCEFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(decoderOSCEFields{}); got != 0 {
		t.Fatalf("decoderOSCEFields size = %d, want 0", got)
	}
	if got := unsafe.Sizeof(decoderOSCEBWEState{}); got != 0 {
		t.Fatalf("decoderOSCEBWEState size = %d, want 0", got)
	}
	if got := unsafe.Sizeof(decoderOSCELACEState{}); got != 0 {
		t.Fatalf("decoderOSCELACEState size = %d, want 0", got)
	}
}
