//go:build !gopus_dred && !gopus_extra_controls

package silk

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildDREDHookStateIsZeroSize(t *testing.T) {
	if dredHooksEnabled {
		t.Fatal("default build unexpectedly enables DRED hooks")
	}
	if nativeLowbandCaptureEnabled {
		t.Fatal("default build unexpectedly enables native lowband capture")
	}
	if got := unsafe.Sizeof(dredHookState{}); got != 0 {
		t.Fatalf("dredHookState size=%d want 0", got)
	}
}
