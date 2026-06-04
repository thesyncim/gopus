//go:build !gopus_extra_controls

package silk

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildNativePostfilterExtraStateIsZeroSize(t *testing.T) {
	if nativePostfilterEnabled {
		t.Fatal("default build unexpectedly enables native postfilter extras")
	}
	if got := unsafe.Sizeof(nativePostfilterExtras{}); got != 0 {
		t.Fatalf("nativePostfilterExtras size=%d want 0", got)
	}
}
