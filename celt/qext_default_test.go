//go:build !gopus_qext
// +build !gopus_qext

package celt

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildQEXTFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(decoderQEXTFields{}); got != 0 {
		t.Fatalf("decoderQEXTFields size=%d want 0", got)
	}
	if got := unsafe.Sizeof(encoderQEXTFields{}); got != 0 {
		t.Fatalf("encoderQEXTFields size=%d want 0", got)
	}
	if got := unsafe.Sizeof(encoderQEXTScratchFields{}); got != 0 {
		t.Fatalf("encoderQEXTScratchFields size=%d want 0", got)
	}
}
