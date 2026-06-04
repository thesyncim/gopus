//go:build !gopus_dred && !gopus_extra_controls

package gopus

import (
	"testing"
	"unsafe"
)

// These tests assert the DRED/OSCE decoder embeds carry no storage in the
// default build, keeping the Decoder struct byte-unchanged when neither
// gopus_dred nor gopus_extra_controls is set (the zero-cost gated-feature
// contract).

func TestDefaultBuildDecoderDREDFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(decoderDREDFields{}); got != 0 {
		t.Fatalf("decoderDREDFields size=%d want 0", got)
	}
}

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

// assertDecoderDREDRuntimeLoadedForTest is the default-build twin of the
// gopus_dred helper: with DRED compiled out there is no neural runtime to
// assert, so it is a no-op that lets shared tests stay tag-agnostic.
func assertDecoderDREDRuntimeLoadedForTest(t testing.TB, _ *Decoder, _ string) {
	t.Helper()
}
