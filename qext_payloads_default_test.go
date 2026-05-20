//go:build !gopus_qext
// +build !gopus_qext

package gopus

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildQEXTPayloadsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(decoderQEXTPayloads{}); got != 0 {
		t.Fatalf("decoderQEXTPayloads size=%d want 0", got)
	}
}
