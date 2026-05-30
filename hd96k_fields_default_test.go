//go:build !gopus_qext

package gopus

import (
	"testing"
	"unsafe"
)

// TestDefaultBuildHD96kFieldsAreZeroSize asserts the native 96 kHz API-rate
// embeds carry no storage in the default build, keeping the Decoder/Encoder
// structs byte-unchanged when gopus_qext is absent.
func TestDefaultBuildHD96kFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(decoderHD96kFields{}); got != 0 {
		t.Fatalf("decoderHD96kFields size = %d, want 0", got)
	}
	if got := unsafe.Sizeof(encoderHD96kFields{}); got != 0 {
		t.Fatalf("encoderHD96kFields size = %d, want 0", got)
	}
}
