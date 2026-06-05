//go:build !gopus_dred && !gopus_osce

package multistream

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildOptionalFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(decoderDREDFields{}); got != 0 {
		t.Fatalf("decoderDREDFields size = %d, want 0", got)
	}
	if got := unsafe.Sizeof(decoderOSCEFields{}); got != 0 {
		t.Fatalf("decoderOSCEFields size = %d, want 0", got)
	}
	if got := unsafe.Sizeof(streamOSCEFields{}); got != 0 {
		t.Fatalf("streamOSCEFields size = %d, want 0", got)
	}
	if got := unsafe.Sizeof(streamOSCEState{}); got != 0 {
		t.Fatalf("streamOSCEState size = %d, want 0", got)
	}
}
