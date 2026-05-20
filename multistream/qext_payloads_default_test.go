//go:build !gopus_qext
// +build !gopus_qext

package multistream

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildQEXTPayloadsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(streamQEXTPayloads{}); got != 0 {
		t.Fatalf("streamQEXTPayloads size=%d want 0", got)
	}
}
